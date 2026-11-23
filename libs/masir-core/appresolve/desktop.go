package appresolve

import (
	"bufio"
	"os"
	"slices"
	"strings"
)

// Desktop is the routing-relevant part of a desktop entry.
type Desktop struct {
	Name       string
	Exec       string
	Type       string
	NoDisplay  bool
	Hidden     bool
	OnlyShowIn []string
	NotShowIn  []string
	path       string
}

// Listed reports whether a menu would show this entry: an application that is
// not hidden from menus. Settings panels, actions and deprecated entries set
// NoDisplay or Hidden and are not apps a user routes.
func (d Desktop) Listed() bool {
	if d.NoDisplay || d.Hidden {
		return false
	}
	if d.Type != "" && d.Type != "Application" {
		return false
	}
	for _, desktop := range strings.Split(os.Getenv("XDG_CURRENT_DESKTOP"), ":") {
		if desktop == "" {
			continue
		}
		if slices.Contains(d.NotShowIn, desktop) {
			return false
		}
		if slices.Contains(d.OnlyShowIn, desktop) {
			return true
		}
	}
	return d.OnlyShowIn == nil
}

// FromDesktop reads the routing and visibility fields of [Desktop Entry].
func FromDesktop(path string) (Desktop, error) {
	f, err := os.Open(path)
	if err != nil {
		return Desktop{}, err
	}
	defer f.Close()
	d := Desktop{path: path}
	section := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		if section != "[Desktop Entry]" {
			continue
		}
		switch {
		case d.Name == "" && strings.HasPrefix(line, "Name="):
			d.Name = strings.TrimSpace(strings.TrimPrefix(line, "Name="))
		case d.Exec == "" && strings.HasPrefix(line, "Exec="):
			d.Exec = strings.TrimSpace(strings.TrimPrefix(line, "Exec="))
		case d.Type == "" && strings.HasPrefix(line, "Type="):
			d.Type = strings.TrimSpace(strings.TrimPrefix(line, "Type="))
		case strings.HasPrefix(line, "OnlyShowIn="):
			d.OnlyShowIn = strings.Split(strings.TrimPrefix(line, "OnlyShowIn="), ";")
		case strings.HasPrefix(line, "NotShowIn="):
			d.NotShowIn = strings.Split(strings.TrimPrefix(line, "NotShowIn="), ";")
		case !d.NoDisplay && strings.HasPrefix(line, "NoDisplay="):
			d.NoDisplay = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(line, "NoDisplay=")), "true")
		case !d.Hidden && strings.HasPrefix(line, "Hidden="):
			d.Hidden = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(line, "Hidden=")), "true")
		}
	}
	return d, sc.Err()
}

// Value resolves the entry's Exec line to a routing value.
func (d Desktop) Value() (string, error) {
	return resolve(d.Exec, d.path)
}
