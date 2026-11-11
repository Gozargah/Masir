package service

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
)

// LegacyLocation only inspects launchd's definition. Call it off the UI thread;
// it never runs the legacy executable in a potentially user-controlled directory.
func LegacyLocation() bool {
	f, e := os.Open(DefinitionPath())
	if e != nil {
		return false
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 65537))
	if e != nil || len(b) > 65536 {
		return false
	}
	path, _, _, e := definitionExecutable(b)
	return e == nil && path == LegacyDarwinBinary
}

// RewriteLegacyDefinition changes only ProgramArguments[0], preserving the served
// user, KeepAlive policy and other settings. Its caller must authenticate the plist
// and the new target before writing this result as root.
func RewriteLegacyDefinition(data []byte) ([]byte, error) {
	path, start, end, e := definitionExecutable(data)
	if e != nil {
		return nil, e
	}
	if path == BinaryPath() {
		return data, nil
	}
	if path != LegacyDarwinBinary {
		return nil, errors.New("unexpected installed launchd executable")
	}
	var replacement bytes.Buffer
	replacement.WriteString("<string>")
	xml.EscapeText(&replacement, []byte(BinaryPath()))
	replacement.WriteString("</string>")
	result := append([]byte{}, data[:start]...)
	result = append(result, replacement.Bytes()...)
	result = append(result, data[end:]...)
	return result, nil
}
func definitionExecutable(data []byte) (string, int64, int64, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	depth := 0
	wantArray, inArray, seenArgs := false, false, false
	var path string
	var begin, end int64
	for {
		before := d.InputOffset()
		token, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", 0, 0, e
		}
		switch v := token.(type) {
		case xml.StartElement:
			depth++
			if depth == 1 && v.Name.Local != "plist" {
				return "", 0, 0, errors.New("invalid launchd plist")
			}
			if depth == 2 && v.Name.Local != "dict" {
				return "", 0, 0, errors.New("invalid launchd dictionary")
			}
			if depth == 3 && v.Name.Local == "key" {
				var key string
				if e := d.DecodeElement(&key, &v); e != nil {
					return "", 0, 0, e
				}
				depth--
				// Program overrides ProgramArguments[0]. Official Masir definitions use
				// only ProgramArguments; reject a custom override rather than leaving an
				// old executable active after the apparent migration.
				if key == "Program" {
					return "", 0, 0, errors.New("launchd Program override is unsupported for offline migration")
				}
				if key == "ProgramArguments" {
					if seenArgs {
						return "", 0, 0, errors.New("duplicate launchd ProgramArguments")
					}
					seenArgs = true
					wantArray = true
				}
				continue
			}
			if wantArray && depth == 3 {
				if v.Name.Local != "array" {
					return "", 0, 0, errors.New("invalid launchd arguments")
				}
				wantArray = false
				inArray = true
				continue
			}
			if inArray && depth == 4 {
				if v.Name.Local != "string" {
					return "", 0, 0, errors.New("invalid launchd executable argument")
				}
				if e := d.DecodeElement(&path, &v); e != nil {
					return "", 0, 0, e
				}
				depth--
				begin, end = before, d.InputOffset()
				inArray = false
			}
		case xml.EndElement:
			if inArray && depth == 3 {
				return "", 0, 0, errors.New("launchd definition has no executable argument")
			}
			depth--
		}
	}
	if path == "" || depth != 0 {
		return "", 0, 0, errors.New("launchd definition has no executable argument")
	}
	return path, begin, end, nil
}
