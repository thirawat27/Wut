package app

import (
	"fmt"

	"github.com/thirawat27/wut/internal/core/config"
)

// SetConfigResult is what changed.
//
// Previous is carried because the useful confirmation is not "capture.tier is
// now T1" but "capture.tier was T0.5 and is now T1" — the second one tells the
// user whether they actually changed anything.
type SetConfigResult struct {
	Key      string        `json:"key"`
	Value    string        `json:"value"`
	Previous string        `json:"previous"`
	Changed  bool          `json:"changed"`
	Path     string        `json:"path"`
	Config   config.Config `json:"-"`
}

// SetConfig applies one configuration key and writes the file.
//
// The order matters: parse, then validate the whole configuration, then write.
// Writing first and validating on the next load is how a tool ends up unable
// to start because of a value it accepted itself.
func (a *App) SetConfig(key, value string) (SetConfigResult, error) {
	writer := a.deps.ConfigWriter
	if writer == nil {
		return SetConfigResult{}, fmt.Errorf("configuration is read-only in this process")
	}

	previous, _ := a.deps.Config.Get(key)

	updated, err := config.Set(a.deps.Config, key, value)
	if err != nil {
		return SetConfigResult{}, err
	}
	if err := writer.Save(updated); err != nil {
		return SetConfigResult{}, err
	}

	// Keep the running process consistent with the file it just wrote. It
	// matters for the daemon, which does not exit after the command.
	a.deps.Config = updated

	now, _ := updated.Get(key)
	return SetConfigResult{
		Key:      key,
		Value:    now,
		Previous: previous,
		Changed:  now != previous,
		Path:     writer.Path(),
		Config:   updated,
	}, nil
}
