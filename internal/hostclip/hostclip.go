// Package hostclip copies an image from the host clipboard to a file the
// container can read. A session runs in a container with no display, so Pi
// cannot read the host clipboard itself; the xclip stand-in in the image
// reads this file instead.
package hostclip

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
)

// File is the name of the clipboard image inside the bridge's directory.
// tools/clipboard/xclip reads the same name.
const File = "image.png"

// Bridge copies the clipboard image into Dir.
type Bridge struct {
	Dir string
	// Read returns the clipboard image as PNG, or nil when there is none.
	Read func() ([]byte, error)
}

// Paste saves the current clipboard image as File. With no image, or when the
// clipboard cannot be read, it removes any earlier one, so a paste never
// attaches a stale image.
func (b *Bridge) Paste() {
	img, err := b.Read()
	if err != nil || len(img) == 0 {
		b.Clear()
		return
	}
	if err := b.write(img); err != nil {
		b.Clear()
	}
}

// Clear removes the saved image.
func (b *Bridge) Clear() {
	os.Remove(filepath.Join(b.Dir, File))
}

// write replaces File in one rename, so the container never reads half an image.
func (b *Bridge) write(img []byte) error {
	if err := os.MkdirAll(b.Dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(b.Dir, ".incoming-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	// The container user is not the host user, so the file must be world-readable.
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(img); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(b.Dir, File))
}

// Read returns the host clipboard image as PNG, or nil when there is none.
func Read() ([]byte, error) {
	if runtime.GOOS == "darwin" {
		return readMac()
	}
	return readLinux()
}

var appleScriptPNG = regexp.MustCompile(`^«data PNGf([0-9A-Fa-f]+)»\s*$`)

// readMac asks AppleScript for the PNG form of the clipboard. It fails when
// the clipboard holds no image.
func readMac() ([]byte, error) {
	out, err := exec.Command("osascript", "-e", "the clipboard as «class PNGf»").Output()
	if err != nil {
		return nil, nil // no image on the clipboard
	}
	return parseAppleScript(string(out))
}

// parseAppleScript decodes the «data PNGf…» text that osascript prints.
func parseAppleScript(out string) ([]byte, error) {
	m := appleScriptPNG.FindStringSubmatch(out)
	if m == nil {
		return nil, errors.New("not a PNG clipboard")
	}
	return hex.DecodeString(m[1])
}

// readLinux tries the Wayland tool, then the X11 one.
func readLinux() ([]byte, error) {
	for _, argv := range [][]string{
		{"wl-paste", "--type", "image/png"},
		{"xclip", "-selection", "clipboard", "-t", "image/png", "-o"},
	} {
		if _, err := exec.LookPath(argv[0]); err != nil {
			continue
		}
		var out bytes.Buffer
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdout = &out
		if cmd.Run() == nil && out.Len() > 0 {
			return out.Bytes(), nil
		}
	}
	return nil, nil
}
