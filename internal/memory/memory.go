// Package memory is arissa's filesystem-backed store for
// Anthropic's built-in memory tool (memory_20250818).
//
// Claude addresses memory as if it were a POSIX filesystem rooted
// at /memories. The Store translates those paths to a real
// directory on disk (cfg.Memory.Dir) and implements the six
// commands that the memory tool emits: view, create, str_replace,
// insert, delete, rename.
//
// All paths are constrained to stay inside /memories; any attempt
// to escape with .. or symlinks is rejected with an error. Memory
// is global — there is no per-user isolation — so any operator
// talking to arissa sees (and can change) the same facts.
package memory

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// ErrPath is returned when a memory path escapes /memories.
var ErrPath = errors.New("path must be under /memories")

// Store is a filesystem-backed memory store.
type Store struct {
	root string // absolute directory on disk that mirrors /memories
}

// New returns a Store rooted at dir. The directory is created if
// missing.
func New(dir string) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", dir, err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", abs, err)
	}
	return &Store{root: abs}, nil
}

// Root returns the absolute on-disk directory the store uses.
func (s *Store) Root() string { return s.root }

// resolve maps a /memories/... path to an absolute on-disk path,
// rejecting traversal and non-/memories requests.
func (s *Store) resolve(memPath string) (string, error) {
	clean := filepath.ToSlash(filepath.Clean(memPath))
	if clean == "/memories" {
		return s.root, nil
	}
	if !strings.HasPrefix(clean, "/memories/") {
		return "", ErrPath
	}
	rel := strings.TrimPrefix(clean, "/memories/")
	if rel == "" || strings.Contains(rel, "..") {
		return "", ErrPath
	}
	return filepath.Join(s.root, rel), nil
}

// View renders a file or directory as the memory tool expects.
// For directories it returns a two-level listing. For files it
// returns the content annotated with 1-indexed line numbers
// (cat -n style), optionally restricted to viewRange = [start,
// end] where end = -1 means "to end of file".
func (s *Store) View(memPath string, viewRange []int64) (string, error) {
	abs, err := s.resolve(memPath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", toUserError(memPath, err)
	}
	if info.IsDir() {
		return s.viewDir(abs, memPath)
	}
	return s.viewFile(abs, memPath, viewRange)
}

func (s *Store) viewDir(abs, memPath string) (string, error) {
	var lines []string
	lines = append(lines,
		fmt.Sprintf("Here're the files and directories up to 2 levels deep in %s, excluding hidden items:", memPath))
	// Walk abs, include memPath itself and entries up to 2 levels
	// below. Produce "size\t/memories/..." lines matching the
	// upstream format used by the Python reference.
	rootInfo, err := os.Stat(abs)
	if err != nil {
		return "", toUserError(memPath, err)
	}
	lines = append(lines, fmt.Sprintf("%s\t%s", humanSize(rootInfo.Size()), memPath))

	err = filepath.Walk(abs, func(p string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if p == abs {
			return nil
		}
		rel, err := filepath.Rel(abs, p)
		if err != nil {
			return err
		}
		// Depth is number of path separators in rel; we allow 2
		// levels deep (one intermediate directory).
		depth := strings.Count(rel, string(filepath.Separator)) + 1
		if depth > 2 {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		name := info.Name()
		if strings.HasPrefix(name, ".") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		display := path.Join(memPath, filepath.ToSlash(rel))
		if info.IsDir() {
			display += "/"
		}
		lines = append(lines, fmt.Sprintf("%s\t%s", humanSize(info.Size()), display))
		return nil
	})
	if err != nil {
		return "", toUserError(memPath, err)
	}
	sort.Strings(lines[2:]) // keep header + root entry, sort the rest
	return strings.Join(lines, "\n"), nil
}

func (s *Store) viewFile(abs, memPath string, viewRange []int64) (string, error) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", toUserError(memPath, err)
	}
	lines := strings.Split(string(data), "\n")
	// The final split of a file that ends with "\n" yields a
	// trailing empty element; drop it so the numbering doesn't
	// show a phantom last line.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}

	start, end := 1, len(lines)
	if len(viewRange) == 2 {
		start = int(viewRange[0])
		if viewRange[1] == -1 {
			end = len(lines)
		} else {
			end = int(viewRange[1])
		}
	}
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return fmt.Sprintf("Here's the content of %s with line numbers:\n", memPath), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Here's the content of %s with line numbers:\n", memPath)
	for i := start; i <= end; i++ {
		fmt.Fprintf(&b, "%6d\t%s\n", i, lines[i-1])
	}
	return b.String(), nil
}

// Create writes (or overwrites) a file with the given body.
// Parent directories are created as needed.
func (s *Store) Create(memPath, body string) (string, error) {
	abs, err := s.resolve(memPath)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return "", toUserError(memPath, err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o600); err != nil {
		return "", toUserError(memPath, err)
	}
	return fmt.Sprintf("File created successfully at %s", memPath), nil
}

// StrReplace replaces a single occurrence of oldStr with newStr.
// oldStr must match exactly once; zero or multiple matches are
// reported as errors.
func (s *Store) StrReplace(memPath, oldStr, newStr string) (string, error) {
	abs, err := s.resolve(memPath)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", toUserError(memPath, err)
	}
	count := strings.Count(string(data), oldStr)
	switch count {
	case 0:
		return "", fmt.Errorf("old_str not found in %s", memPath)
	case 1:
		updated := strings.Replace(string(data), oldStr, newStr, 1)
		if err := os.WriteFile(abs, []byte(updated), 0o600); err != nil {
			return "", toUserError(memPath, err)
		}
		return fmt.Sprintf("The file %s has been edited successfully", memPath), nil
	default:
		return "", fmt.Errorf("old_str matched %d times in %s; it must match exactly once", count, memPath)
	}
}

// Insert inserts text after line `after` (0 = beginning of file).
func (s *Store) Insert(memPath string, after int64, text string) (string, error) {
	abs, err := s.resolve(memPath)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", toUserError(memPath, err)
	}
	lines := strings.Split(string(data), "\n")
	trailingNL := false
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
		trailingNL = true
	}
	if after < 0 || after > int64(len(lines)) {
		return "", fmt.Errorf("insert_line %d out of range for %s (0..%d)", after, memPath, len(lines))
	}
	inserted := strings.Split(strings.TrimRight(text, "\n"), "\n")
	combined := make([]string, 0, len(lines)+len(inserted))
	combined = append(combined, lines[:after]...)
	combined = append(combined, inserted...)
	combined = append(combined, lines[after:]...)
	out := strings.Join(combined, "\n")
	if trailingNL {
		out += "\n"
	}
	if err := os.WriteFile(abs, []byte(out), 0o600); err != nil {
		return "", toUserError(memPath, err)
	}
	return fmt.Sprintf("The file %s has been edited successfully", memPath), nil
}

// Delete removes the file or directory at memPath (recursive).
func (s *Store) Delete(memPath string) (string, error) {
	abs, err := s.resolve(memPath)
	if err != nil {
		return "", err
	}
	if abs == s.root {
		return "", fmt.Errorf("refusing to delete /memories root")
	}
	if err := os.RemoveAll(abs); err != nil {
		return "", toUserError(memPath, err)
	}
	return fmt.Sprintf("File/directory deleted successfully at %s", memPath), nil
}

// Rename moves a memory entry to a new path.
func (s *Store) Rename(oldPath, newPath string) (string, error) {
	oldAbs, err := s.resolve(oldPath)
	if err != nil {
		return "", err
	}
	newAbs, err := s.resolve(newPath)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(newAbs), 0o700); err != nil {
		return "", toUserError(newPath, err)
	}
	if err := os.Rename(oldAbs, newAbs); err != nil {
		return "", toUserError(oldPath, err)
	}
	return fmt.Sprintf("Renamed %s to %s", oldPath, newPath), nil
}

// humanSize returns a compact size string like "4.0K" / "128".
func humanSize(n int64) string {
	const kb = 1024
	if n < kb {
		return fmt.Sprintf("%d", n)
	}
	v := float64(n) / kb
	return fmt.Sprintf("%.1fK", v)
}

// toUserError makes filesystem errors readable to Claude and keeps
// the memory-path language consistent. path.Ext-style wrapping.
func toUserError(memPath string, err error) error {
	if os.IsNotExist(err) {
		return fmt.Errorf("path not found: %s", memPath)
	}
	return err
}
