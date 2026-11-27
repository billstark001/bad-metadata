// Package cleaner discovers and cleans metadata without depending on a user interface.
package cleaner

import (
	"encoding/json"
	"io"
	"io/fs"
	"time"
)

type Kind string

const (
	AppleDouble Kind = "appledouble"
	DSStore     Kind = "dsstore"
	Spotlight   Kind = "spotlight"
	FSEvents    Kind = "fsevents"
	Trashes     Kind = "trashes"
)

func DefaultKinds() []Kind { return []Kind{AppleDouble, DSStore, Spotlight, FSEvents} }

type Options struct {
	// Nil uses the defaults; an empty non-nil slice selects nothing.
	Kinds []Kind `json:"kinds"`
}

type Item struct {
	Path        string `json:"path"`
	Kind        Kind   `json:"kind"`
	IsDir       bool   `json:"is_dir"`
	Bytes       int64  `json:"bytes"`
	Fingerprint string `json:"fingerprint"`
}

type Issue struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

type Plan struct {
	Root      string    `json:"root"`
	CreatedAt time.Time `json:"created_at"`
	Items     []Item    `json:"items"`
	Issues    []Issue   `json:"issues"`
	Bytes     int64     `json:"bytes"`
	rootInfo  fs.FileInfo
	// Retain a private copy so exported, mutable preview fields cannot expand a plan.
	approved []Item
	ready    bool
}

type Mode string

const (
	Quarantine Mode = "quarantine"
	Delete     Mode = "delete"
)

type Result struct {
	Session   string   `json:"session,omitempty"`
	Completed []string `json:"completed"`
	Issues    []Issue  `json:"issues"`
	Cancelled bool     `json:"cancelled"`
}

const quarantineDir = ".badmeta-quarantine"

func WriteJSON(w io.Writer, value any) error {
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	return e.Encode(value)
}
