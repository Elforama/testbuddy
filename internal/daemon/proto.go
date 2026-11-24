package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

type Request struct {
	Cmd  string          `json:"cmd"`
	Args json.RawMessage `json:"args,omitempty"`
	Cwd  string          `json:"cwd,omitempty"`
}

type Response struct {
	OK     bool            `json:"ok"`
	Exit   int             `json:"exit"`
	Text   string          `json:"text,omitempty"`
	JSON   json.RawMessage `json:"json,omitempty"`
	Image  []byte          `json:"image,omitempty"`
	More   bool            `json:"more,omitempty"`
	Stderr bool            `json:"stderr,omitempty"`
}

func Dir() string {
	if d := os.Getenv("TB_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".testbuddy")
}

func SocketPath() string {
	p := filepath.Join(Dir(), "tb.sock")
	if len(p) < 100 {
		return p
	}
	sum := sha256.Sum256([]byte(Dir()))
	return filepath.Join(os.TempDir(), "tb-"+hex.EncodeToString(sum[:6])+".sock")
}
func LogPath() string { return filepath.Join(Dir(), "daemon.log") }
