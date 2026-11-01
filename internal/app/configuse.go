package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jorben/tacet/internal/config"
	"github.com/jorben/tacet/internal/store"
)

const maxConfigObjects = 1000

func configKind(k string) (config.Kind, bool) {
	switch config.Kind(k) {
	case config.RuleSet, config.Scheme, config.Preset, config.ModelProfile:
		return config.Kind(k), true
	}
	return "", false
}

func mapConfigErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, config.ErrMissing):
		return ErrNotFound
	case errors.Is(err, config.ErrConflict):
		return ErrVersion
	case errors.Is(err, config.ErrContent), errors.Is(err, config.ErrSchema):
		return ErrInvalid
	case errors.Is(err, config.ErrReadOnly):
		return ErrNotAllowed
	}
	return err
}

func itemOf(o config.Object) ConfigItem {
	return ConfigItem{ID: o.ID, Kind: string(o.Kind), Name: o.Name, Note: o.Note, Version: o.Version, ReadOnly: o.ReadOnly, Content: string(o.Content), MinConfidenceReset: o.MinConfidenceReset}
}

func (e *Engine) ConfigList(ctx context.Context, kind string) ([]ConfigItem, error) {
	k, ok := configKind(kind)
	if !ok {
		return nil, ErrInvalid
	}
	var out []ConfigItem
	err := e.ws.Commit(ctx, func(tx store.Tx) error {
		after := ""
		for len(out) < maxConfigObjects {
			page, err := e.cfg.List(tx, k, after, 200)
			if err != nil {
				return err
			}
			if len(page) == 0 {
				return nil
			}
			for _, s := range page {
				o, err := e.cfg.Get(tx, s.ID)
				if err != nil {
					return err
				}
				out = append(out, itemOf(o))
				after = s.ID
			}
		}
		return nil
	})
	return out, mapConfigErr(err)
}

func newConfigID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "cfg_" + hex.EncodeToString(b[:]), nil
}

func (e *Engine) ConfigCreate(ctx context.Context, kind, name, note, content string) (ConfigItem, error) {
	k, ok := configKind(kind)
	if !ok {
		return ConfigItem{}, ErrInvalid
	}
	id, err := newConfigID()
	if err != nil {
		return ConfigItem{}, err
	}
	var out config.Object
	err = e.ws.Commit(ctx, func(tx store.Tx) (err error) {
		out, err = e.cfg.Create(tx, id, k, name, note, []byte(content))
		return
	})
	if err != nil {
		return ConfigItem{}, mapConfigErr(err)
	}
	return itemOf(out), nil
}

func (e *Engine) ConfigSave(ctx context.Context, id string, expected uint64, name, note, content string) (ConfigItem, error) {
	var out config.Object
	err := e.ws.Commit(ctx, func(tx store.Tx) (err error) {
		out, err = e.cfg.Save(tx, id, expected, []byte(content), name, note)
		return
	})
	if err != nil {
		return ConfigItem{}, mapConfigErr(err)
	}
	return itemOf(out), nil
}

func (e *Engine) ConfigCopy(ctx context.Context, id, name string) (ConfigItem, error) {
	var out config.Object
	err := e.ws.Commit(ctx, func(tx store.Tx) (err error) {
		out, err = e.cfg.Copy(tx, id, name)
		return
	})
	return itemOf(out), mapConfigErr(err)
}

func (e *Engine) ConfigHistory(ctx context.Context, id string) ([]ConfigVersion, error) {
	var out []ConfigVersion
	err := e.ws.Commit(ctx, func(tx store.Tx) error {
		vs, err := e.cfg.History(tx, id)
		for _, v := range vs {
			out = append(out, ConfigVersion{Number: v.Number, SavedAt: v.SavedAt.Unix()})
		}
		return err
	})
	return out, mapConfigErr(err)
}

func (e *Engine) ConfigDelete(ctx context.Context, id string, expected uint64) error {
	err := e.ws.Commit(ctx, func(tx store.Tx) error { return e.cfg.Delete(tx, id, expected) })
	if errors.Is(err, config.ErrInUse) {
		return fmt.Errorf("%w: %s", ErrNotAllowed, "CONFIG_IN_USE")
	}
	return mapConfigErr(err)
}

func (e *Engine) ConfigRollback(ctx context.Context, id string, expected, restore uint64) (ConfigItem, error) {
	var out config.Object
	err := e.ws.Commit(ctx, func(tx store.Tx) (err error) {
		cur, err := e.cfg.Get(tx, id)
		if err != nil {
			return err
		}
		if cur.Kind == config.Preset {
			out, err = e.rollbackPreset(tx, cur, expected, restore)
			return err
		}
		out, err = e.cfg.Rollback(tx, id, expected, restore)
		return
	})
	return itemOf(out), mapConfigErr(err)
}

func (e *Engine) SettingsGet(ctx context.Context) (Settings, error) {
	var out Settings
	err := e.ws.Commit(ctx, func(tx store.Tx) error {
		s, err := e.cfg.Settings(tx)
		out = Settings{CloseBehavior: s.CloseBehavior, Theme: s.Theme, RedactStyle: s.RedactStyle, ResourceTier: s.ResourceTier, RetentionDays: s.RetentionDays, AutoUpdate: s.AutomaticUpdateChecks,
			GPUAcceleration: s.GPUAcceleration}
		return err
	})
	return out, mapConfigErr(err)
}

func (e *Engine) SettingsSave(ctx context.Context, in Settings) (Settings, error) {
	err := e.ws.Commit(ctx, func(tx store.Tx) error {
		cur, err := e.cfg.Settings(tx)
		if err != nil {
			return err
		}
		cur.CloseBehavior, cur.Theme, cur.RedactStyle, cur.ResourceTier, cur.RetentionDays, cur.AutomaticUpdateChecks, cur.GPUAcceleration =
			in.CloseBehavior, in.Theme, in.RedactStyle, in.ResourceTier, in.RetentionDays, in.AutoUpdate, in.GPUAcceleration
		return e.cfg.SaveSettings(tx, cur)
	})
	if err != nil {
		return Settings{}, mapConfigErr(err)
	}
	return e.SettingsGet(ctx)
}

func (e *Engine) Plugins(ctx context.Context) ([]PluginInfo, error) {
	byID := map[string]*PluginInfo{}
	add := func(p plugin, formats []string) {
		x, ok := byID[p.id.ID]
		if !ok {
			x = &PluginInfo{ID: p.id.ID, Version: p.id.Version, Roles: append([]string(nil), p.man.Roles...), Builtin: true, Enabled: true}
			byID[p.id.ID] = x
		}
		x.Formats = append(x.Formats, formats...)
	}
	for f, p := range e.plugins {
		add(p, []string{f})
	}
	for _, m := range e.models {
		add(m.plug, nil)
	}
	out := make([]PluginInfo, 0, len(byID))
	for _, x := range byID {
		sort.Strings(x.Formats)
		out = append(out, *x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// rollbackPreset brings a preset back to an earlier version together with the scheme it was saved with,
// in one transaction: either both are back or neither. A version saved before schemes were recorded
// (or whose scheme version is no longer kept) is refused when the scheme would have to differ, rather
// than leaving a preset next to a scheme it never ran with.
func (e *Engine) rollbackPreset(tx store.Tx, cur config.Object, expected, restore uint64) (config.Object, error) {
	if cur.ReadOnly {
		return config.Object{}, config.ErrReadOnly
	}
	if cur.Version != expected {
		return config.Object{}, config.ErrConflict
	}
	old, err := e.cfg.Content(tx, cur.ID, restore)
	if err != nil {
		return config.Object{}, err
	}
	var p config.PresetContent
	if json.Unmarshal(old, &p) != nil {
		return config.Object{}, config.ErrContent
	}
	if p.SchemeRev != 0 {
		scheme, err := e.cfg.Get(tx, p.SchemeID)
		if err != nil {
			return config.Object{}, err
		}
		if scheme.Version != p.SchemeRev {
			if scheme, err = e.cfg.Rollback(tx, p.SchemeID, scheme.Version, p.SchemeRev); err != nil {
				return config.Object{}, err
			}
		}
		p.SchemeRev = scheme.Version // the scheme's new version is the one this preset version now runs with
		if old, err = json.Marshal(p); err != nil {
			return config.Object{}, config.ErrContent
		}
	}
	return e.cfg.Save(tx, cur.ID, expected, old, cur.Name, cur.Note)
}
