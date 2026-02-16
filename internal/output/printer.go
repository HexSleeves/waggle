package output

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pterm/pterm"
)

// Printer wraps pterm for styled output, respecting output modes.
// All methods are no-ops in TUI, JSON, or Quiet mode.
type Printer struct {
	mode    Mode
	verbose bool
	writer  io.Writer
}

// NewPrinter creates a Printer for the given output mode.
func NewPrinter(mode Mode, verbose bool) *Printer {
	return &Printer{
		mode:    mode,
		verbose: verbose,
		writer:  os.Stdout,
	}
}

// NewPrinterWithWriter creates a Printer with a custom writer (for testing).
func NewPrinterWithWriter(mode Mode, verbose bool, w io.Writer) *Printer {
	return &Printer{
		mode:    mode,
		verbose: verbose,
		writer:  w,
	}
}

// active returns true if this printer should produce output.
func (p *Printer) active() bool {
	return p.mode == ModePlain
}

// Header prints a large styled header.
func (p *Printer) Header(text string) {
	if !p.active() {
		return
	}
	pterm.DefaultHeader.
		WithWriter(p.writer).
		WithBackgroundStyle(pterm.NewStyle(pterm.BgCyan)).
		WithTextStyle(pterm.NewStyle(pterm.FgBlack, pterm.Bold)).
		Println(text)
}

// Section prints a section header.
func (p *Printer) Section(text string) {
	if !p.active() {
		return
	}
	pterm.DefaultSection.
		WithWriter(p.writer).
		Println(text)
}

// Info prints an informational message.
func (p *Printer) Info(format string, args ...interface{}) {
	if !p.active() {
		return
	}
	pterm.Info.WithWriter(p.writer).Printfln(format, args...)
}

// Success prints a success message.
func (p *Printer) Success(format string, args ...interface{}) {
	if !p.active() {
		return
	}
	pterm.Success.WithWriter(p.writer).Printfln(format, args...)
}

// Warning prints a warning message.
func (p *Printer) Warning(format string, args ...interface{}) {
	if !p.active() {
		return
	}
	pterm.Warning.WithWriter(p.writer).Printfln(format, args...)
}

// Error prints an error message.
func (p *Printer) Error(format string, args ...interface{}) {
	if !p.active() {
		return
	}
	pterm.Error.WithWriter(p.writer).Printfln(format, args...)
}

// Debug prints a debug message (only if verbose).
func (p *Printer) Debug(format string, args ...interface{}) {
	if !p.active() || !p.verbose {
		return
	}
	dbg := &pterm.PrefixPrinter{
		Prefix: pterm.Prefix{
			Text:  " DEBUG ",
			Style: pterm.NewStyle(pterm.BgGray, pterm.FgWhite),
		},
		Writer: p.writer,
	}
	dbg.Printfln(format, args...)
}

// Table prints a table with headers and rows.
func (p *Printer) Table(headers []string, rows [][]string) {
	if !p.active() {
		return
	}
	data := pterm.TableData{headers}
	data = append(data, rows...)
	pterm.DefaultTable.
		WithWriter(p.writer).
		WithHasHeader().
		WithData(data).
		Render() //nolint:errcheck
}

// BulletItem is an item for a bullet list.
type BulletItem struct {
	Level int
	Icon  string
	Text  string
	Style *pterm.Style
}

// BulletList prints a bullet list.
func (p *Printer) BulletList(items []BulletItem) {
	if !p.active() {
		return
	}
	var bulletItems []pterm.BulletListItem
	for _, item := range items {
		bi := pterm.BulletListItem{
			Level:  item.Level,
			Text:   item.Text,
			Bullet: item.Icon,
		}
		if item.Style != nil {
			bi.TextStyle = item.Style
		}
		bulletItems = append(bulletItems, bi)
	}
	pterm.DefaultBulletList.
		WithWriter(p.writer).
		WithItems(bulletItems).
		Render() //nolint:errcheck
}

// TreeNode is a node for a tree.
type TreeNode struct {
	Text     string
	Children []TreeNode
}

func toPtermTree(node TreeNode) pterm.TreeNode {
	tn := pterm.TreeNode{Text: node.Text}
	for _, c := range node.Children {
		tn.Children = append(tn.Children, toPtermTree(c))
	}
	return tn
}

// Tree prints a tree.
func (p *Printer) Tree(root TreeNode) {
	if !p.active() {
		return
	}
	pterm.DefaultTree.
		WithWriter(p.writer).
		WithRoot(toPtermTree(root)).
		Render() //nolint:errcheck
}

// SpinnerHandle wraps a pterm spinner.
type SpinnerHandle struct {
	spinner *pterm.SpinnerPrinter
}

// Stop stops the spinner with a success message.
func (h *SpinnerHandle) Stop(msg string) {
	if h == nil || h.spinner == nil {
		return
	}
	h.spinner.Success(msg)
}

// Fail stops the spinner with an error message.
func (h *SpinnerHandle) Fail(msg string) {
	if h == nil || h.spinner == nil {
		return
	}
	h.spinner.Fail(msg)
}

// Spinner starts a spinner with the given text.
func (p *Printer) Spinner(text string) *SpinnerHandle {
	if !p.active() {
		return nil
	}
	sp, _ := pterm.DefaultSpinner.
		WithWriter(p.writer).
		Start(text)
	return &SpinnerHandle{spinner: sp}
}

// KeyValue prints key-value pairs in a formatted way.
func (p *Printer) KeyValue(pairs [][]string) {
	if !p.active() {
		return
	}
	for _, pair := range pairs {
		if len(pair) == 2 {
			fmt.Fprintf(p.writer, "  %s  %s\n",
				pterm.LightCyan(pair[0]+":"),
				pair[1])
		}
	}
}

// Println prints a plain line.
func (p *Printer) Println(text string) {
	if !p.active() {
		return
	}
	fmt.Fprintln(p.writer, text)
}

// Printf prints a plain formatted line.
func (p *Printer) Printf(format string, args ...interface{}) {
	if !p.active() {
		return
	}
	fmt.Fprintf(p.writer, format, args...)
}

// StatusIcon returns a colored status icon string.
func StatusIcon(status string) string {
	switch status {
	case "complete":
		return pterm.Green("✔")
	case "running":
		return pterm.Cyan("●")
	case "pending":
		return pterm.Gray("○")
	case "failed":
		return pterm.Red("✖")
	case "cancelled":
		return pterm.Yellow("⊘")
	case "retrying":
		return pterm.Yellow("↻")
	default:
		return pterm.Gray("?")
	}
}

// Divider prints a horizontal rule.
func (p *Printer) Divider() {
	if !p.active() {
		return
	}
	fmt.Fprintln(p.writer, pterm.Gray(strings.Repeat("─", 50)))
}
