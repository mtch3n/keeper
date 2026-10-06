package detect_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// keeper decides without a model, so the judge package is gone and nothing
// reaches for it.
func Test_JDG_C7_TheJudgePackageIsGone(t *testing.T) {
	if _, err := os.Stat("../judge"); err == nil {
		t.Error("internal/judge still exists")
	}
	_ = filepath.WalkDir(filepath.Join("..", ".."), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() && (d.Name() == "node_modules" || d.Name() == "web") {
			return filepath.SkipDir
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		b, _ := os.ReadFile(path)
		if strings.Contains(string(b), `"github.com/mtchen/keeper/internal/`+`judge"`) {
			t.Errorf("%s imports internal/judge", path)
		}
		return nil
	})
}
