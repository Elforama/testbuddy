// Package daemon is the long-lived `tb` background process. It keeps one connection per
// device open so agent commands (`tb look`, `tb do`) cost a few milliseconds instead of a
// device handshake each. The CLI starts it on demand and talks to it over a unix socket.
package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// Request is one CLI call. Args are command-specific.
type Request struct {
	Cmd  string          `json:"cmd"`
	Args json.RawMessage `json:"args,omitempty"`
	Cwd  string          `json:"cwd,omitempty"`
}

// Response is the answer. Streaming commands send several with More=true, then a final one.
type Response struct {
	OK    bool            `json:"ok"`
	Exit  int             `json:"exit"`
	Text  string          `json:"text,omitempty"`
	JSON  json.RawMessage `json:"json,omitempty"`
	Image []byte          `json:"image,omitempty"`
	More  bool            `json:"more,omitempty"`
}

// Dir is Test Buddy's state directory (~/.testbuddy).
func Dir() string {
	if d := os.Getenv("TB_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".testbuddy")
}

// SocketPath is where the daemon listens. Unix socket paths are limited to ~104 bytes, so a
// long state directory gets its socket in the temp folder, named by a hash of the directory.
func SocketPath() string {
	p := filepath.Join(Dir(), "tb.sock")
	if len(p) < 100 {
		return p
	}
	sum := sha256.Sum256([]byte(Dir()))
	return filepath.Join(os.TempDir(), "tb-"+hex.EncodeToString(sum[:6])+".sock")
}
func LogPath() string { return filepath.Join(Dir(), "daemon.log") }
