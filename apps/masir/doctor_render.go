package main

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/Gozargah/Masir/libs/masir-core/doctor"
)

// The design system: warm charcoal, ONE accent (terracotta), letterspaced
// small-caps labels, hairline rules. Problems get the accent; healthy rows stay
// quiet so the eye lands on what is wrong.
const (
	escReset     = "\x1b[0m"
	escBold      = "\x1b[1m"
	escAccent    = "\x1b[38;2;213;116;85m"  // #d57455 terracotta — fills, marks
	escAccentInk = "\x1b[38;2;224;138;109m" // #e08a6d contrast-safe accent text
	escWarm      = "\x1b[38;2;195;194;183m" // #c3c2b7 warm gray — warnings
	escInk       = "\x1b[38;2;236;233;226m" // #ece9e2 primary text
	escMid       = "\x1b[38;2;181;178;168m" // #b5b2a8 secondary text
	escFaint     = "\x1b[38;2;133;130;122m" // #85827a tertiary text
	escRule      = "\x1b[38;2;53;52;47m"    // #35342f hairline

	ruleWidth = 96
	wrapWidth = 96
)

type theme struct{ on bool }

func newTheme() theme {
	return theme{on: term.IsTerminal(int(os.Stdout.Fd())) &&
		os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"}
}

func (t theme) paint(code, s string) string {
	if !t.on || s == "" {
		return s
	}
	return code + s + escReset
}

func (t theme) accent(s string) string    { return t.paint(escAccent, s) }
func (t theme) accentInk(s string) string { return t.paint(escAccentInk, s) }
func (t theme) warm(s string) string      { return t.paint(escWarm, s) }
func (t theme) ink(s string) string       { return t.paint(escInk, s) }
func (t theme) mid(s string) string       { return t.paint(escMid, s) }
func (t theme) faint(s string) string     { return t.paint(escFaint, s) }
func (t theme) rule(s string) string      { return t.paint(escRule, s) }
func (t theme) strong(s string) string    { return t.paint(escBold+escInk, s) }

// printDoctor renders the report: a header, one letterspaced block per group,
// and a summary line that also says what the exit code will be.
func printDoctor(reports []doctor.Report, verbose bool, versionText string) {
	t := newTheme()

	rows := make([]doctor.Report, 0, len(reports))
	for _, r := range reports {
		if r.State == doctor.Skip && !verbose {
			continue
		}
		rows = append(rows, r)
	}

	fmt.Println()
	fmt.Printf("  %s%s\n", t.paint(escBold+escAccent, "masir doctor"),
		t.faint("   "+versionText+"   read-only"))
	fmt.Printf("  %s\n", t.rule(strings.Repeat("─", ruleWidth)))

	titleW := 12
	for _, r := range rows {
		if n := utf8.RuneCountInString(r.Title); n > titleW {
			titleW = n
		}
	}

	var order []string
	byGroup := make(map[string][]doctor.Report, 3)
	for _, r := range rows {
		g := r.Group
		if g == "" {
			g = "General"
		}
		if _, seen := byGroup[g]; !seen {
			order = append(order, g)
		}
		byGroup[g] = append(byGroup[g], r)
	}

	problems, warnings := 0, 0
	for i, g := range order {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("  %s\n", t.faint(spaceCaps(g)))
		for _, r := range byGroup[g] {
			mark, markColor, titleColor, detailColor := "–", escRule, escFaint, escFaint
			switch r.State {
			case doctor.OK:
				mark, markColor, titleColor, detailColor = "✓", escFaint, escMid, escMid
			case doctor.Warn:
				mark, markColor, titleColor, detailColor = "!", escWarm, escInk, escMid
				warnings++
			case doctor.Fail:
				mark, markColor, titleColor, detailColor = "✗", escAccent, escInk, escInk
				problems++
			}
			title := fmt.Sprintf("%-*s", titleW, r.Title)
			indent := strings.Repeat(" ", titleW+7)
			detailLines := wrapText(r.Detail, wrapWidth-titleW-7)
			fmt.Printf("  %s  %s  %s\n", t.paint(markColor, mark), t.paint(titleColor, title), t.paint(detailColor, detailLines[0]))
			for _, extra := range detailLines[1:] {
				fmt.Printf("%s%s\n", indent, t.paint(detailColor, extra))
			}
			if r.Fix != "" && (r.State == doctor.Warn || r.State == doctor.Fail) {
				for i, line := range wrapText(r.Fix, wrapWidth-8) {
					label := "fix"
					if i > 0 {
						label = "   "
					}
					fmt.Printf("     %s  %s\n", t.accentInk(label), t.faint(line))
				}
			}
		}
	}

	fmt.Printf("  %s\n", t.rule(strings.Repeat("─", ruleWidth)))
	fmt.Printf("  %s%s\n\n", summaryLine(t, problems, warnings), t.faint(rightPadExit(problems, warnings)))
}

// summaryLine is the verdict: the accent when something is broken, warm gray
// for warnings only, quiet when everything passes.
func summaryLine(t theme, problems, warnings int) string {
	var parts []string
	if problems > 0 {
		word := "problems"
		if problems == 1 {
			word = "problem"
		}
		parts = append(parts, fmt.Sprintf("%d %s", problems, word))
	}
	if warnings > 0 {
		word := "warnings"
		if warnings == 1 {
			word = "warning"
		}
		parts = append(parts, fmt.Sprintf("%d %s", warnings, word))
	}
	if len(parts) == 0 {
		return t.ink("all clear")
	}
	line := strings.Join(parts, "  ·  ")
	if problems > 0 {
		return t.paint(escBold+escAccent, line)
	}
	return t.warm(line)
}

// rightPadExit puts the exit code at the right edge of the rule.
func rightPadExit(problems, warnings int) string {
	code := "0"
	switch {
	case problems > 0:
		code = "2"
	case warnings > 0:
		code = "1"
	}
	return fmt.Sprintf("%sexit %s", strings.Repeat(" ", max(0, ruleWidth-4-len("exit ")-len(code))), code)
}

// spaceCaps is the terminal form of the design system's letterspaced labels.
func spaceCaps(s string) string {
	return strings.Join(strings.Split(strings.ToUpper(s), ""), " ")
}

// wrapText breaks a sentence at word boundaries, never mid-word unless a single
// word is longer than the width.
func wrapText(s string, width int) []string {
	if width < 20 {
		width = 20
	}
	var lines []string
	line := ""
	for _, word := range strings.Fields(s) {
		switch {
		case line == "":
			line = word
		case utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) <= width:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
