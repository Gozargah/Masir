package appresolve

import (
	"bufio"
	"os"
	"strings"
)

// Desktop is the routing-relevant part of a desktop entry.
type Desktop struct {
	Name string
	Exec string
}

// FromDesktop reads the Name and Exec lines of the [Desktop Entry] group.
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
		}
	}
	return d, sc.Err()
}

// Value resolves the entry's Exec line to a routing value.
func (d Desktop) Value() (string, error) {
	return Resolve(d.Exec)
}
