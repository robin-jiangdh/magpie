package checkin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/yetone/magpie/internal/appdir"
)

// Credential is one provider's check-in credential. It lives apart from
// providers.json and never mixes with a model's API key.
type Credential struct {
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
	AccessToken string `json:"accessToken,omitempty"`
	Session     string `json:"session,omitempty"` // pasted session cookie / refresh cookie
	APIUser     string `json:"apiUser,omitempty"` // new-api-user id, some sites require it
}

// Empty says the credential carries nothing worth keeping.
func (c Credential) Empty() bool {
	return c.Username == "" && c.Password == "" && c.AccessToken == "" &&
		c.Session == "" && c.APIUser == ""
}

// Path is the file the check-in credentials live in, by provider id.
func Path() string { return filepath.Join(appdir.Config(), "checkin-credentials.json") }

var mu sync.Mutex

// load reads the credentials file. A missing file is no credentials, not
// an error; an unreadable or unparsable file is an error — it must never
// read as "no credentials" and silently drop what was saved.
func load() (map[string]Credential, error) {
	b, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Credential{}, nil
		}
		return nil, fmt.Errorf("读签到凭据文件失败: %v", err)
	}
	var m map[string]Credential
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 {
		return map[string]Credential{}, nil
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("签到凭据文件损坏: %v", err)
	}
	if m == nil {
		m = map[string]Credential{}
	}
	return m, nil
}

// store writes the credentials file with owner-only permissions.
func store(m map[string]Credential) error {
	if err := os.MkdirAll(filepath.Dir(Path()), 0o700); err != nil {
		return fmt.Errorf("创建配置目录失败: %v", err)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(Path(), b, 0o600); err != nil {
		return fmt.Errorf("写签到凭据文件失败: %v", err)
	}
	// WriteFile only applies the mode on creation; make sure a file that
	// already existed with wider permissions is tightened.
	if err := os.Chmod(Path(), 0o600); err != nil {
		return fmt.Errorf("设置签到凭据文件权限失败: %v", err)
	}
	return nil
}

// Get returns the provider's saved check-in credential. An unreadable
// file is an error, never "no credential".
func Get(id string) (Credential, bool, error) {
	mu.Lock()
	defer mu.Unlock()
	m, err := load()
	if err != nil {
		return Credential{}, false, err
	}
	c, ok := m[id]
	return c, ok && !c.Empty(), nil
}

// Has says a check-in credential is saved for the provider. It never
// reveals the credential itself.
func Has(id string) (bool, error) {
	_, ok, err := Get(id)
	return ok, err
}

// Save stores the provider's check-in credential. An empty credential
// deletes the saved one.
func Save(id string, c Credential) error {
	if id == "" {
		return fmt.Errorf("缺少供应商 id")
	}
	mu.Lock()
	defer mu.Unlock()
	m, err := load()
	if err != nil {
		return err
	}
	if c.Empty() {
		delete(m, id)
	} else {
		m[id] = c
	}
	return store(m)
}

// Delete drops the provider's saved check-in credential.
func Delete(id string) error {
	mu.Lock()
	defer mu.Unlock()
	m, err := load()
	if err != nil {
		return err
	}
	delete(m, id)
	return store(m)
}
