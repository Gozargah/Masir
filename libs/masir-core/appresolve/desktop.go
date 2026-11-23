package appresolve

import (
	"bufio"
	"os"
	"strings"
)

// Desktop is the routing-relevant part of a desktop entry.
type Desktop struct {
	Name      string
	Exec      string
	Type      string
	NoDisplay bool
	Hidden    bool
}

// Listed reports whether a menu would show this entry: an application that is
// not hidden from menus. Settings panels, actions and deprecated entries set
// NoDisplay or Hidden and are not apps a user routes.
func (d Desktop) Listed() bool {
	if d.NoDisplay || d.Hidden {
		return false
	}
	return d.Type == "" || d.Type == "Application"
}

// FromDesktop reads the Name, Exec, Type, NoDisplay and Hidden lines of the
// [Desktop Entry] group.
func FromDesktop(path string) (Desktop, error) {
	f, err := os.Open(path)
	if err != nil {
		return Desktop{}, err
	}
	defer f.Close()
	var d Desktop
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
	return Resolve(d.Exec)
}
