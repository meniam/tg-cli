package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func (d *daemon) accountJournal(record map[string]any) (err error) {
	d.deletionsMu.Lock()
	defer d.deletionsMu.Unlock()
	if d.opts.Paths.Dir == "" {
		return fmt.Errorf("account journal path is empty")
	}
	f, err := os.OpenFile(filepath.Join(d.opts.Paths.Dir, "account.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if e := f.Close(); err == nil {
			err = e
		}
	}()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if err = json.NewEncoder(f).Encode(record); err != nil {
		return err
	}
	return f.Sync()
}
