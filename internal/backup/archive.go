package backup

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var ErrForceRequired = errors.New("restore target already contains Webdock data; use --force")

type Options struct {
	WithSites bool
	WithLogs  bool
}

func Create(root, destination string, options Options) error {
	items := []string{"webdock.env", "config", "data"}
	if options.WithSites {
		items = append(items, "sites")
	}
	if options.WithLogs {
		items = append(items, "logs")
	}
	for _, item := range items {
		if _, err := os.Lstat(filepath.Join(root, item)); err != nil {
			return fmt.Errorf("backup item %s: %w", item, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".webdock-backup-*")
	if err != nil {
		return fmt.Errorf("create temporary archive: %w", err)
	}
	tmpName := tmp.Name()
	complete := false
	defer func() {
		_ = tmp.Close()
		if !complete {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod temporary archive: %w", err)
	}
	gz := gzip.NewWriter(tmp)
	tw := tar.NewWriter(gz)
	for _, item := range items {
		base := filepath.Join(root, item)
		if err := filepath.WalkDir(base, func(current string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, current)
			if err != nil {
				return err
			}
			if isRuntimeArtifact(filepath.ToSlash(rel), info.Mode()) {
				return nil
			}
			link := ""
			if info.Mode()&os.ModeSymlink != 0 {
				link, err = os.Readlink(current)
				if err != nil {
					return err
				}
			}
			header, err := tar.FileInfoHeader(info, link)
			if err != nil {
				return err
			}
			header.Name = filepath.ToSlash(rel)
			header.Uid = 0
			header.Gid = 0
			header.Uname = ""
			header.Gname = ""
			if err := tw.WriteHeader(header); err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			file, err := os.Open(current)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(tw, file)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			return closeErr
		}); err != nil {
			return fmt.Errorf("archive %s: %w", item, err)
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar archive: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("close gzip archive: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync archive: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close archive: %w", err)
	}
	if err := os.Link(tmpName, destination); err != nil {
		return fmt.Errorf("install archive %s without overwriting: %w", destination, err)
	}
	complete = true
	_ = os.Remove(tmpName)
	return nil
}

func isRuntimeArtifact(rel string, mode os.FileMode) bool {
	if mode&os.ModeSocket != 0 {
		return true
	}
	return strings.HasSuffix(rel, ".sock") ||
		strings.HasSuffix(rel, ".sock.lock") ||
		strings.HasSuffix(rel, ".pid")
}

func Restore(root, archive string, force bool) error {
	return restore(root, archive, force, nil)
}

// RestoreValidated restores an archive only when validate accepts the staged
// instance. This lets callers reject malformed archives before replacing any
// existing data.
func RestoreValidated(root, archive string, force bool, validate func(string) error) error {
	return restore(root, archive, force, validate)
}

func restore(root, archive string, force bool, validate func(string) error) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create instance directory: %w", err)
	}
	targets := []string{"webdock.env", "config", "data", "sites", "logs"}
	if !force {
		for _, name := range targets {
			if _, err := os.Lstat(filepath.Join(root, name)); err == nil {
				return ErrForceRequired
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("inspect restore target %s: %w", name, err)
			}
		}
	}
	stage, err := os.MkdirTemp(root, ".restore-stage-")
	if err != nil {
		return fmt.Errorf("create restore staging directory: %w", err)
	}
	defer os.RemoveAll(stage)
	if err := extract(archive, stage); err != nil {
		return err
	}
	for _, item := range []struct {
		name string
		dir  bool
	}{
		{name: "webdock.env"},
		{name: "config", dir: true},
		{name: "data", dir: true},
	} {
		info, err := os.Lstat(filepath.Join(stage, item.name))
		if err != nil {
			return fmt.Errorf("archive is missing %s: %w", item.name, err)
		}
		if item.dir && !info.IsDir() {
			return fmt.Errorf("archive %s is not a directory", item.name)
		}
		if !item.dir && !info.Mode().IsRegular() {
			return fmt.Errorf("archive %s is not a regular file", item.name)
		}
	}
	if validate != nil {
		if err := validate(stage); err != nil {
			return fmt.Errorf("validate archive: %w", err)
		}
	}

	old, err := os.MkdirTemp(root, ".restore-old-")
	if err != nil {
		return fmt.Errorf("create restore rollback directory: %w", err)
	}
	defer os.RemoveAll(old)
	type moved struct {
		name   string
		backup string
	}
	var moves []moved
	rollback := func() error {
		var errs []error
		for i := len(moves) - 1; i >= 0; i-- {
			target := filepath.Join(root, moves[i].name)
			if err := os.RemoveAll(target); err != nil {
				errs = append(errs, fmt.Errorf("remove %s: %w", target, err))
			}
			if moves[i].backup != "" {
				if err := os.Rename(moves[i].backup, target); err != nil {
					errs = append(errs, fmt.Errorf("restore %s: %w", target, err))
				}
			}
		}
		return errors.Join(errs...)
	}
	rollbackError := func(cause error) error {
		if err := rollback(); err != nil {
			return fmt.Errorf("%v; rollback: %w", cause, err)
		}
		return cause
	}
	for _, name := range targets {
		source := filepath.Join(stage, name)
		if _, err := os.Lstat(source); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return rollbackError(fmt.Errorf("inspect staged %s: %w", name, err))
		}
		target := filepath.Join(root, name)
		oldPath := ""
		if _, err := os.Lstat(target); err == nil {
			oldPath = filepath.Join(old, name)
			if err := os.Rename(target, oldPath); err != nil {
				return rollbackError(fmt.Errorf("move existing %s: %w", name, err))
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return rollbackError(fmt.Errorf("inspect target %s: %w", name, err))
		}
		moves = append(moves, moved{name: name, backup: oldPath})
		if err := os.Rename(source, target); err != nil {
			return rollbackError(fmt.Errorf("install restored %s: %w", name, err))
		}
	}
	return nil
}

func extract(archive, destination string) error {
	file, err := os.Open(archive)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open gzip archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	seen := make(map[string]bool)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		name, err := validateEntry(header)
		if err != nil {
			return err
		}
		if seen[name] {
			return fmt.Errorf("archive contains duplicate path %q", name)
		}
		seen[name] = true
		target := filepath.Join(destination, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if err := ensureParentPath(destination, name); err != nil {
				return err
			}
			if info, err := os.Lstat(target); err == nil {
				if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
					return fmt.Errorf("archive directory %q conflicts with an existing entry", name)
				}
				continue
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("inspect directory %s: %w", name, err)
			}
			if err := os.MkdirAll(target, os.FileMode(header.Mode).Perm()); err != nil {
				return fmt.Errorf("create directory %s: %w", name, err)
			}
		case tar.TypeReg:
			if err := ensureParentPath(destination, name); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("create parent for %s: %w", name, err)
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(header.Mode).Perm())
			if err != nil {
				return fmt.Errorf("create file %s: %w", name, err)
			}
			_, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil {
				return fmt.Errorf("extract %s: %w", name, copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close %s: %w", name, closeErr)
			}
		case tar.TypeSymlink:
			if err := ensureParentPath(destination, name); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("create parent for symlink %s: %w", name, err)
			}
			if _, err := os.Lstat(target); err == nil {
				return fmt.Errorf("archive symlink %q conflicts with an existing entry", name)
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("inspect symlink %s: %w", name, err)
			}
			if err := os.Symlink(header.Linkname, target); err != nil {
				return fmt.Errorf("create symlink %s: %w", name, err)
			}
		default:
			return fmt.Errorf("archive entry %q has unsupported type %d", name, header.Typeflag)
		}
	}
}

func ensureParentPath(root, name string) error {
	parts := strings.Split(name, "/")
	current := root
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect archive parent %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive path %q traverses symlink %q", name, current)
		}
		if !info.IsDir() {
			return fmt.Errorf("archive path %q has non-directory parent %q", name, current)
		}
	}
	return nil
}

func validateEntry(header *tar.Header) (string, error) {
	name := path.Clean(header.Name)
	if name == "." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
		return "", fmt.Errorf("unsafe archive path %q", header.Name)
	}
	allowed := name == "webdock.env" ||
		name == "config" || strings.HasPrefix(name, "config/") ||
		name == "data" || strings.HasPrefix(name, "data/") ||
		name == "sites" || strings.HasPrefix(name, "sites/") ||
		name == "logs" || strings.HasPrefix(name, "logs/")
	if !allowed {
		return "", fmt.Errorf("archive path %q is outside the backup set", name)
	}
	if header.Typeflag == tar.TypeSymlink {
		if path.IsAbs(header.Linkname) {
			return "", fmt.Errorf("symlink %q has an absolute target", name)
		}
		resolved := path.Clean(path.Join(path.Dir(name), header.Linkname))
		if resolved == ".." || strings.HasPrefix(resolved, "../") {
			return "", fmt.Errorf("symlink %q escapes the instance", name)
		}
	}
	return name, nil
}
