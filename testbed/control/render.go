package control

import (
	"os"
	"path/filepath"
)

// atomicWriteFile writes a file atomically using a rename: a temporary file in the same directory, written, set to mode
// 0644 and renamed over the target. A reader, or a watcher, sees the old file or the new one, never part of a write.
// It is the launchpad's code.
func atomicWriteFile(filename string, data []byte) error {
	tmpFile, err := os.CreateTemp(filepath.Dir(filename), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()

	cleanup := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
	}

	if _, err := tmpFile.Write(data); err != nil {
		cleanup()
		return err
	}

	if err := tmpFile.Chmod(0o644); err != nil {
		cleanup()
		return err
	}

	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpName) // File is closed, just remove.
		return err
	}

	if err := os.Rename(tmpName, filename); err != nil {
		_ = os.Remove(tmpName) // File is closed, just remove.
		return err
	}

	return nil
}

// render writes the document to the render file, creating its directory. It does nothing when rendering is off.
func (s *Server) render(document []byte) error {
	if s.cfg.Render == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.cfg.Render), os.ModePerm); err != nil {
		return err
	}
	return atomicWriteFile(s.cfg.Render, document)
}

// unrender removes the render file. If it cannot be removed it did not exist in the first place, so the error is
// ignored, as the launchpad does.
func (s *Server) unrender() {
	if s.cfg.Render != "" {
		_ = os.Remove(s.cfg.Render)
	}
}
