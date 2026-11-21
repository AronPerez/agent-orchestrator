// Command stampcheck prints the value a built Go binary actually holds in
// string variables that frontend/scripts/build-daemon.mjs sets with -ldflags -X.
//
// Searching the binary for the stamped text is not enough: Go records the whole
// -ldflags string in the binary's build info, so the text is present even when
// the -X target is misspelled or the variable was renamed and the linker
// silently ignored it. stampcheck reads the variable itself, through its symbol
// and string header, so a stale target shows up as the variable's default.
//
// Usage:
//
//	stampcheck <binary> <package-path.Var>...
//
// Each variable prints as "<package-path.Var>=<value>" on its own line. A
// variable missing from the symbol table is an error.
package main

import (
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: stampcheck <binary> <package-path.Var>...")
		os.Exit(2)
	}
	img, err := openImage(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "stampcheck:", err)
		os.Exit(1)
	}
	defer img.close()
	for _, name := range os.Args[2:] {
		value, err := img.readString(name)
		if err != nil {
			fmt.Fprintln(os.Stderr, "stampcheck:", err)
			os.Exit(1)
		}
		fmt.Printf("%s=%s\n", name, value)
	}
}

// section is one loaded region of the binary's address space.
type section struct {
	addr, size uint64
	zero       bool // zero-filled at load time (bss): no bytes in the file
	readAt     func(p []byte, off int64) (int, error)
}

type image struct {
	order    binary.ByteOrder
	symbols  map[string]uint64
	sections []section
	close    func() error
}

// openImage loads the symbol table and sections of an ELF or Mach-O binary.
func openImage(path string) (*image, error) {
	if f, err := elf.Open(path); err == nil {
		return elfImage(f)
	}
	if f, err := macho.Open(path); err == nil {
		return machoImage(f)
	}
	return nil, fmt.Errorf("%s: not an ELF or Mach-O binary", path)
}

func elfImage(f *elf.File) (*image, error) {
	if f.Class != elf.ELFCLASS64 {
		f.Close()
		return nil, errors.New("only 64-bit binaries are supported")
	}
	syms, err := f.Symbols()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("read symbols: %w", err)
	}
	img := &image{order: f.ByteOrder, symbols: map[string]uint64{}, close: f.Close}
	for _, s := range syms {
		img.symbols[s.Name] = s.Value
	}
	for _, s := range f.Sections {
		if s.Flags&elf.SHF_ALLOC == 0 {
			continue
		}
		img.sections = append(img.sections, section{addr: s.Addr, size: s.Size, zero: s.Type == elf.SHT_NOBITS, readAt: s.ReadAt})
	}
	return img, nil
}

func machoImage(f *macho.File) (*image, error) {
	if f.Magic != macho.Magic64 {
		f.Close()
		return nil, errors.New("only 64-bit binaries are supported")
	}
	if f.Symtab == nil {
		f.Close()
		return nil, errors.New("binary has no symbol table (built with -s?)")
	}
	img := &image{order: f.ByteOrder, symbols: map[string]uint64{}, close: f.Close}
	for _, s := range f.Symtab.Syms {
		img.symbols[s.Name] = s.Value
	}
	const sZerofill, sThreadLocalZerofill = 0x1, 0x12
	for _, s := range f.Sections {
		kind := s.Flags & 0xff
		img.sections = append(img.sections, section{addr: s.Addr, size: s.Size, zero: kind == sZerofill || kind == sThreadLocalZerofill, readAt: s.ReadAt})
	}
	return img, nil
}

// read returns n bytes at virtual address addr.
func (img *image) read(addr, n uint64) ([]byte, error) {
	for _, s := range img.sections {
		if addr < s.addr || addr+n > s.addr+s.size {
			continue
		}
		buf := make([]byte, n)
		if s.zero {
			return buf, nil
		}
		if _, err := s.readAt(buf, int64(addr-s.addr)); err != nil {
			return nil, err
		}
		return buf, nil
	}
	return nil, fmt.Errorf("address %#x+%d is not in any loaded section", addr, n)
}

// readString follows a Go string variable's header (data pointer, length) to
// its bytes. Mach-O prefixes C-visible symbol names with "_", so try both.
func (img *image) readString(name string) (string, error) {
	addr, ok := img.symbols[name]
	if !ok {
		addr, ok = img.symbols["_"+name]
	}
	if !ok {
		return "", fmt.Errorf("symbol %s not found (renamed, or removed as unused?)", name)
	}
	header, err := img.read(addr, 16)
	if err != nil {
		return "", fmt.Errorf("%s header: %w", name, err)
	}
	ptr, length := img.order.Uint64(header[:8]), img.order.Uint64(header[8:])
	if length == 0 {
		return "", nil
	}
	if length > 1<<20 {
		return "", fmt.Errorf("%s: implausible string length %d", name, length)
	}
	data, err := img.read(ptr, length)
	if err != nil {
		return "", fmt.Errorf("%s data: %w", name, err)
	}
	return string(data), nil
}
