package sessionartifacts

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const planApprovalPinsDir = "plan-approval-pins"

var linkPlanApproval = func(root *os.Root, oldname, newname string) error {
	return root.Link(oldname, newname)
}

func planApprovalPinPath(id string) (string, bool) {
	if id == "" || filepath.Base(id) != id || !filepath.IsLocal(id) {
		return "", false
	}
	return filepath.Join(VersionsDir, planApprovalPinsDir, id), true
}

// PinPlanApproval keeps one AO-owned hard link to a plan approval. Keeping the
// inode alive prevents a later file from reusing it on file systems that expose
// no birth time or inode generation, while the link's current identity still
// follows edits and renames of the approved plan.
func PinPlanApproval(dir string, row domain.SessionArtifactRecord) error {
	if row.Role != domain.SessionArtifactRolePlanApproval || row.File.Ino == 0 {
		return nil
	}
	rel, ok := RelPath(dir, row.Path)
	if !ok || rel != row.Path {
		return ErrNotRegistrable
	}
	pin, ok := planApprovalPinPath(row.ID)
	if !ok {
		return ErrNotRegistrable
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()

	sourceInfo, err := root.Lstat(filepath.FromSlash(rel))
	if err != nil || !sourceInfo.Mode().IsRegular() {
		if err != nil {
			return err
		}
		return ErrNotRegistrable
	}
	if !row.File.SameFile(fullIdentity(filepath.Join(dir, filepath.FromSlash(rel)), sourceInfo)) {
		return domain.ErrSessionArtifactsChanged
	}
	if err := ensurePinDir(root, VersionsDir); err != nil {
		return err
	}
	if err := ensurePinDir(root, filepath.Join(VersionsDir, planApprovalPinsDir)); err != nil {
		return err
	}

	created := false
	if err := linkPlanApproval(root, filepath.FromSlash(rel), pin); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return nil
		}
		pinInfo, pinErr := root.Lstat(pin)
		currentInfo, sourceErr := root.Lstat(filepath.FromSlash(rel))
		if pinErr != nil || sourceErr != nil || !pinInfo.Mode().IsRegular() || !currentInfo.Mode().IsRegular() {
			return domain.ErrSessionArtifactsChanged
		}
		if !os.SameFile(pinInfo, currentInfo) {
			// An explicit registration already made row.File authoritative for this
			// path. Rotate its private witness; a failure leaves no false match and
			// the same registration can safely retry.
			if err := root.Remove(pin); err != nil {
				return err
			}
			if err := linkPlanApproval(root, filepath.FromSlash(rel), pin); err != nil {
				return nil
			}
			created = true
		}
	} else {
		created = true
	}
	pinInfo, pinErr := root.Lstat(pin)
	currentInfo, sourceErr := root.Lstat(filepath.FromSlash(rel))
	if pinErr != nil || sourceErr != nil || !pinInfo.Mode().IsRegular() || !currentInfo.Mode().IsRegular() ||
		!os.SameFile(pinInfo, currentInfo) ||
		!row.File.SameFile(fullIdentity(filepath.Join(dir, pin), pinInfo)) {
		if created {
			_ = root.Remove(pin)
		}
		return domain.ErrSessionArtifactsChanged
	}
	return nil
}

// RefreshPlanApprovalPins makes each plan approval's private witness name its
// currently stored file. File systems without hard-link support simply keep no
// witness; stable identity metadata remains the primary rename signal.
func RefreshPlanApprovalPins(dir string, rows []domain.SessionArtifactRecord) error {
	for _, row := range rows {
		if row.Role != domain.SessionArtifactRolePlanApproval {
			continue
		}
		if row.File.Ino == 0 {
			if err := RemovePlanApprovalPin(dir, row.ID); err != nil {
				return err
			}
			continue
		}
		if err := PinPlanApproval(dir, row); err != nil {
			return err
		}
	}
	return nil
}

// RemovePlanApprovalPin removes the private witness for a deleted plan
// approval. A missing artifact directory or witness is already clean.
func RemovePlanApprovalPin(dir, id string) error {
	pin, ok := planApprovalPinPath(id)
	if !ok {
		return ErrNotRegistrable
	}
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err := root.Remove(pin); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func ensurePinDir(root *os.Root, name string) error {
	if err := root.Mkdir(name, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrNotRegistrable
	}
	return nil
}

func loadPlanApprovalPins(dir string) map[string]domain.FileIdentity {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil
	}
	defer func() { _ = root.Close() }()
	pinDir := filepath.Join(VersionsDir, planApprovalPinsDir)
	info, err := root.Lstat(pinDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	d, err := root.Open(pinDir)
	if err != nil {
		return nil
	}
	defer func() { _ = d.Close() }()
	entries, err := d.ReadDir(MaxFiles)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil
	}
	pins := make(map[string]domain.FileIdentity, len(entries))
	for _, entry := range entries {
		entryInfo, err := entry.Info()
		if err != nil || !entryInfo.Mode().IsRegular() {
			continue
		}
		pins[entry.Name()] = fullIdentity(filepath.Join(dir, pinDir, entry.Name()), entryInfo)
	}
	return pins
}
