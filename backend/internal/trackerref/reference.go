// Package trackerref resolves durable issue references without depending on a
// provider client. It keeps manual session references, migration-era rows, and
// automated intake identities comparable against the project's actual origin.
package trackerref

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Canonical returns the durable provider-scoped identity used by tracker
// intake. GitLab self-managed hosts are part of the identity; gitlab.com and
// GitHub use the provider-native identity alone.
func Canonical(id domain.TrackerID) domain.IssueID {
	provider := id.Provider
	if provider == "" {
		provider = domain.TrackerProviderGitHub
	}
	native := strings.TrimSpace(id.Native)
	if native == "" {
		return ""
	}
	if provider == domain.TrackerProviderGitHub {
		native = strings.ToLower(native)
	}
	host := normalizeHost(provider, id.Host)
	if host != "" {
		return domain.IssueID(string(provider) + ":" + native + "@" + host)
	}
	return domain.IssueID(string(provider) + ":" + native)
}

// Resolve turns a canonical id, provider URL, GitHub native id, or bare issue
// number into one durable identity. Bare numbers are scoped only through the
// project's actual SCM origin; tracker-intake provider/repository overrides do
// not change what a pre-existing manual session reference means.
func Resolve(raw domain.IssueID, project domain.ProjectRecord) (domain.IssueID, bool) {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "", false
	}
	if id, ok := parseCanonical(text); ok {
		return Canonical(id), true
	}
	if id, ok := parseIssueURL(text); ok {
		return Canonical(id), true
	}
	if native, ok := parseGitHubNative(text); ok {
		return Canonical(domain.TrackerID{Provider: domain.TrackerProviderGitHub, Native: native}), true
	}
	number, ok := parsePositiveNumber(strings.TrimPrefix(text, "#"))
	if !ok {
		return "", false
	}
	repo, ok := RepositoryFromOrigin(project.RepoOriginURL)
	if !ok {
		return "", false
	}
	return Canonical(domain.TrackerID{
		Provider: repo.Provider,
		Native:   fmt.Sprintf("%s#%d", repo.Native, number),
		Host:     repo.Host,
	}), true
}

// Equivalent reports whether existing identifies canonical in project scope.
func Equivalent(existing, canonical domain.IssueID, project domain.ProjectRecord) bool {
	want, ok := Resolve(canonical, project)
	if !ok {
		return strings.TrimSpace(string(existing)) == strings.TrimSpace(string(canonical))
	}
	got, ok := Resolve(existing, project)
	return ok && got == want
}

// RepositoryFromOrigin derives tracker scope from the registered SCM origin.
// It accepts HTTPS, ssh://, and common git@host:path remotes.
func RepositoryFromOrigin(raw string) (domain.TrackerRepo, bool) {
	remote := strings.TrimSpace(raw)
	if remote == "" {
		return domain.TrackerRepo{}, false
	}
	if strings.HasPrefix(remote, "git@") && !strings.Contains(remote, "://") {
		if _, rest, ok := strings.Cut(remote, "@"); ok {
			if host, path, ok := strings.Cut(rest, ":"); ok {
				remote = "ssh://git@" + host + "/" + path
			}
		}
	}
	u, err := url.Parse(remote)
	if err != nil || u.Host == "" {
		return domain.TrackerRepo{}, false
	}
	provider := domain.InferTrackerProvider(raw)
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return domain.TrackerRepo{}, false
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return domain.TrackerRepo{}, false
		}
	}
	parts[len(parts)-1] = strings.TrimSuffix(parts[len(parts)-1], ".git")
	if parts[len(parts)-1] == "" || (provider == domain.TrackerProviderGitHub && len(parts) != 2) {
		return domain.TrackerRepo{}, false
	}
	return domain.TrackerRepo{
		Provider: provider,
		Native:   strings.Join(parts, "/"),
		Host:     normalizeHost(provider, urlHost(u)),
	}, true
}

func parseCanonical(raw string) (domain.TrackerID, bool) {
	providerText, scoped, ok := strings.Cut(strings.TrimSpace(raw), ":")
	if !ok {
		return domain.TrackerID{}, false
	}
	provider := domain.TrackerProvider(strings.ToLower(strings.TrimSpace(providerText)))
	if provider != domain.TrackerProviderGitHub && provider != domain.TrackerProviderGitLab {
		return domain.TrackerID{}, false
	}
	host := ""
	if at := strings.LastIndexByte(scoped, '@'); at > strings.LastIndexByte(scoped, '#') {
		host = scoped[at+1:]
		scoped = scoped[:at]
	}
	if provider == domain.TrackerProviderGitHub && strings.TrimSpace(host) != "" {
		return domain.TrackerID{}, false
	}
	if _, ok := issueNativeNumber(scoped); !ok {
		return domain.TrackerID{}, false
	}
	return domain.TrackerID{Provider: provider, Native: strings.TrimSpace(scoped), Host: host}, true
}

func parseGitHubNative(raw string) (string, bool) {
	if strings.Contains(raw, "://") {
		return "", false
	}
	hash := strings.LastIndexByte(raw, '#')
	if hash <= 0 || hash == len(raw)-1 {
		return "", false
	}
	repo := strings.Split(strings.Trim(raw[:hash], "/"), "/")
	if len(repo) != 2 || strings.TrimSpace(repo[0]) == "" || strings.TrimSpace(repo[1]) == "" {
		return "", false
	}
	number, ok := parsePositiveNumber(raw[hash+1:])
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%s/%s#%d", strings.TrimSpace(repo[0]), strings.TrimSuffix(strings.TrimSpace(repo[1]), ".git"), number), true
}

func parseIssueURL(raw string) (domain.TrackerID, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return domain.TrackerID{}, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if strings.EqualFold(u.Hostname(), "github.com") {
		if len(parts) != 4 || parts[2] != "issues" {
			return domain.TrackerID{}, false
		}
		number, ok := parsePositiveNumber(parts[3])
		if !ok {
			return domain.TrackerID{}, false
		}
		return domain.TrackerID{
			Provider: domain.TrackerProviderGitHub,
			Native:   fmt.Sprintf("%s/%s#%d", parts[0], strings.TrimSuffix(parts[1], ".git"), number),
		}, true
	}
	separator := -1
	for i := range parts {
		if i+2 < len(parts) && parts[i] == "-" && parts[i+1] == "issues" {
			separator = i
			break
		}
	}
	if separator < 2 || separator+3 != len(parts) {
		return domain.TrackerID{}, false
	}
	number, ok := parsePositiveNumber(parts[separator+2])
	if !ok {
		return domain.TrackerID{}, false
	}
	project := append([]string(nil), parts[:separator]...)
	project[len(project)-1] = strings.TrimSuffix(project[len(project)-1], ".git")
	return domain.TrackerID{
		Provider: domain.TrackerProviderGitLab,
		Native:   fmt.Sprintf("%s#%d", strings.Join(project, "/"), number),
		Host:     normalizeHost(domain.TrackerProviderGitLab, urlHost(u)),
	}, true
}

func issueNativeNumber(native string) (int, bool) {
	hash := strings.LastIndexByte(native, '#')
	if hash <= 0 || hash == len(native)-1 || strings.TrimSpace(native[:hash]) == "" {
		return 0, false
	}
	return parsePositiveNumber(native[hash+1:])
}

func parsePositiveNumber(raw string) (int, bool) {
	number, err := strconv.Atoi(strings.TrimSpace(raw))
	return number, err == nil && number > 0
}

func normalizeHost(provider domain.TrackerProvider, raw string) string {
	if provider != domain.TrackerProviderGitLab {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(raw))
	if host == "gitlab.com" || host == "www.gitlab.com" {
		return ""
	}
	return host
}

func urlHost(u *url.URL) string {
	host := u.Hostname()
	if port := u.Port(); port != "" {
		return net.JoinHostPort(host, port)
	}
	return host
}
