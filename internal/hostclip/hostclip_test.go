package hostclip

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestParseAppleScriptPNG(t *testing.T) {
	got, err := parseAppleScript("«data PNGf89504E47»\n")
	if err != nil || !bytes.Equal(got, []byte{0x89, 'P', 'N', 'G'}) {
		t.Errorf("got %x err %v", got, err)
	}
	for _, bad := range []string{"", "text", "«data PNGfZZ»"} {
		if _, err := parseAppleScript(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestPasteSavesTheImageForTheContainer(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "clipboard")
	b := &Bridge{Dir: dir, Read: func() ([]byte, error) { return []byte("png"), nil }}
	b.Paste()
	got, err := os.ReadFile(filepath.Join(dir, File))
	if err != nil || string(got) != "png" {
		t.Fatalf("got %q err %v", got, err)
	}
	if st, _ := os.Stat(filepath.Join(dir, File)); st.Mode().Perm() != 0o644 {
		t.Errorf("mode %v: the container user must be able to read it", st.Mode())
	}
}

func TestPasteWithoutAnImageRemovesTheOldOne(t *testing.T) {
	for name, read := range map[string]func() ([]byte, error){
		"no image": func() ([]byte, error) { return nil, nil },
		"error":    func() ([]byte, error) { return nil, errors.New("boom") },
	} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, File), []byte("old"), 0o644)
		(&Bridge{Dir: dir, Read: read}).Paste()
		if _, err := os.Stat(filepath.Join(dir, File)); !os.IsNotExist(err) {
			t.Errorf("%s: the old image stayed: %v", name, err)
		}
	}
}

func TestClearRemovesTheImage(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, File), []byte("old"), 0o644)
	(&Bridge{Dir: dir}).Clear()
	if _, err := os.Stat(filepath.Join(dir, File)); !os.IsNotExist(err) {
		t.Errorf("the image stayed: %v", err)
	}
}
