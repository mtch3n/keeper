package detect_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// keeper authors no detection, so the package that once did is gone and
// nothing reaches for it.
func Test_DET_C23_TheRulesPackageIsGone(t *testing.T) {
	if _, err := os.Stat("../rules"); err == nil {
		t.Error("internal/rules still exists")
	}
	root := filepath.Join("..", "..")
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() && (d.Name() == "node_modules" || d.Name() == "web") {
			return filepath.SkipDir
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		b, _ := os.ReadFile(path)
		if strings.Contains(string(b), `"github.com/mtchen/keeper/internal/`+`rules"`) {
			t.Errorf("%s imports internal/rules", path)
		}
		return nil
	})
}
