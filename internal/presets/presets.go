// Package presets is an agent preset: which prompt sections, context files
// and tools an agent's requests carry, and the patch that turns one set
// into another (DeepSeek Harness's agent presets and cordis.patch.yml,
// which magpie absorbs here — see docs/subsystems/agent-presets.md).
//
// A preset is a named list of patches. A patch says one thing about one
// thing: a system-prompt section to add, drop or replace; a context file
// or a tool to allow, deny, rename or compact. Nothing here executes
// anything: it edits the body an agent sent, in the agent's own API, the
// same bytes the vendor would have been given — which is why a patch is
// cheap to explain, and why a preset that does nothing is nothing but an
// empty list of patches.
//
// Three kinds, as the window shows them:
//
//	提示词 prompt   the system prompt's sections: append, prepend, set, drop
//	代码   context  the files an agent read in (CLAUDE.md, AGENTS.md…):
//	                allow, deny, compact (keep the path, drop the text)
//	工具   tools    the tools offered: allow, deny, rename, compact
//
// Every patch is counted as it is applied or skipped, so the window can
// show "16/19 · 跳过 3" for each kind: what this request's preset did to
// it, and what it left alone. A patch is skipped when it names something
// the request doesn't carry, when another patch of the same kind already
// acted on it, or when the vendor's API can't express it (a Responses
// request has no top-level system prompt to prepend to).
//
// Applying a preset never changes: the model, the stream flag, the tool
// declarations' names (a rename changes the name the model is shown, and
// the call is mapped back on the reply), or anything magpie needs to route
// the request. It is a body edit and nothing more.
package presets

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Kind is what a patch acts on, and the three counters the window shows.
const (
	KindPrompt  = "prompt"
	KindContext = "context"
	KindTools   = "tools"
)

// Kinds, in the order the window shows them.
var Kinds = []string{KindPrompt, KindContext, KindTools}

// Action is what a patch does.
const (
	// prompt
	ActAppend  = "append"  // the text goes after the system prompt's sections
	ActPrepend = "prepend" // before them
	ActSet     = "set"     // the section named by Match becomes Value
	ActDrop    = "drop"    // the section named by Match goes
	// context
	ActAllow = "allow" // the file named by Match is kept, any other dropped
	ActDeny  = "deny"  // the file named by Match goes
	ActCompactContext = "compact" // the file keeps its path and loses its text
	// tools
	ActRename  = "rename"  // the tool named by Match is shown as Value
	ActCompactTools = "compact" // the tool's schema keeps names, loses descriptions
	ActIsolate = "isolate" // the group named by Match is left out (dsh's isolate)
)

// Patch is one edit of a preset.
type Patch struct {
	// ID names the patch within its preset, as dsh's plugin rows are
	// named; the window shows it.
	ID string `json:"id"`
	// Kind is KindPrompt, KindContext or KindTools.
	Kind string `json:"kind"`
	// Action is one of the Act… constants.
	Action string `json:"action"`
	// Match names what the action acts on: the system section's tag
	// ("skills", "magic-docs"), a file's path pattern (CLAUDE.md, **/*.md),
	// or a tool's name or pattern (read, mcp__*, Read|Write).
	Match string `json:"match,omitempty"`
	// Value is the text a set or append adds, the name a rename gives, or
	// nothing.
	Value string `json:"value,omitempty"`
	// Disabled keeps the patch in the preset without applying it. It is
	// counted as skipped.
	Disabled bool `json:"disabled"`
	// Why says, in the window, why a patch a preset ships is off (a
	// platform's tool, a vendor that refuses a shape).
	Why string `json:"why,omitempty"`
}

// Preset is a named set of patches.
type Preset struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Sub is one line about it, as the preset cards show.
	Sub string `json:"sub,omitempty"`
	// About is the "模式说明" text: how it works and when to use it.
	About string `json:"about,omitempty"`
	// Version is the preset's own, as its card shows ("v1.1.71" in dsh:
	// the bundle's). magpie's built-ins carry magpie's version.
	Version string `json:"version,omitempty"`
	// Builtin is a preset magpie ships. A built-in may be edited, and the
	// edit is kept beside it (Saved); it may not be removed.
	Builtin bool `json:"builtin"`
	// Order is where the card sits, low first.
	Order int `json:"order,omitempty"`
	// Patches are applied in order, within a kind as the window lists them.
	Patches []Patch `json:"patches,omitempty"`
	// Isolate are the parts of the session a preset takes over, as dsh's
	// isolate map: "compaction", "planMode", "terminals", "toolResultPruner".
	// magpie reads them where it has the notion (compaction is the agent's
	// own; the gateway only reports them), so a preset's isolate is shown
	// and handed to the agent's own settings where magpie writes any.
	Isolate map[string]bool `json:"isolate,omitempty"`
	// Disabled leaves the preset in the list without being selectable.
	Disabled bool `json:"disabled"`
}

// Stats is what a preset did to one request, per kind: how many of its
// patches applied, how many were skipped, and the total of each kind the
// preset carries (so the window can say 16/19 · 跳过 3).
type Stats struct {
	Prompt  Count `json:"prompt"`
	Context Count `json:"context"`
	Tools   Count `json:"tools"`
	// Applied and Total are every kind together: the "61/66" of a card.
	Applied int `json:"applied"`
	Total   int `json:"total"`
	// Skipped is Total minus Applied.
	Skipped int `json:"skipped"`
	// BytesIn and BytesOut are the body's size before and after.
	BytesIn  int `json:"bytesIn"`
	BytesOut int `json:"bytesOut"`
	// Notes are why patches were skipped, one line each, newest last and
	// at most notesKeep of them.
	Notes []string `json:"notes,omitempty"`
}

// Count is one kind's own tally.
type Count struct {
	Applied  int `json:"applied"`
	Total    int `json:"total"`
	Skipped  int `json:"skipped"`
}

// notesKeep bounds Stats.Notes.
const notesKeep = 8

func (s *Stats) add(kind string, applied bool, note string) {
	var c *Count
	switch kind {
	case KindPrompt:
		c = &s.Prompt
	case KindContext:
		c = &s.Context
	case KindTools:
		c = &s.Tools
	default:
		return
	}
	c.Total++
	s.Total++
	if applied {
		c.Applied++
		s.Applied++
		return
	}
	c.Skipped++
	s.Skipped++
	if note != "" && len(s.Notes) < notesKeep {
		s.Notes = append(s.Notes, note)
	}
}

// Counts fills Skipped on each kind after a run.
func (s *Stats) Settle() {
	s.Prompt.Skipped = s.Prompt.Total - s.Prompt.Applied
	s.Context.Skipped = s.Context.Total - s.Context.Applied
	s.Tools.Skipped = s.Tools.Total - s.Tools.Applied
	s.Skipped = s.Total - s.Applied
}

// Statics are the facts about a preset the window shows before anything has
// run: every kind's total, so a card can say what it carries.
func (p Preset) Statics() Stats {
	var s Stats
	for _, pa := range p.Patches {
		s.add(pa.Kind, false, "")
	}
	s.Settle()
	s.Applied, s.Skipped = 0, 0
	s.Prompt.Applied, s.Context.Applied, s.Tools.Applied = 0, 0, 0
	s.Prompt.Skipped, s.Context.Skipped, s.Tools.Skipped = 0, 0, 0
	return s
}

// Valid says why a preset can't be saved, nil when it can.
func (p Preset) Valid() error {
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("a preset needs an id")
	}
	if strings.ContainsAny(p.ID, " \t/\\") {
		return fmt.Errorf("a preset's id is one word: %q", p.ID)
	}
	seen := map[string]bool{}
	for _, pa := range p.Patches {
		if strings.TrimSpace(pa.ID) == "" {
			return fmt.Errorf("every patch of %s needs an id", p.ID)
		}
		if seen[pa.ID] {
			return fmt.Errorf("%s has two patches called %q", p.ID, pa.ID)
		}
		seen[pa.ID] = true
		if !known(Kinds, pa.Kind) {
			return fmt.Errorf("%q is no kind of patch: one of %s", pa.Kind, strings.Join(Kinds, ", "))
		}
		if !known(actionsOf(pa.Kind), pa.Action) {
			return fmt.Errorf("%q is nothing to do with a %s patch: one of %s", pa.Action, pa.Kind, strings.Join(actionsOf(pa.Kind), ", "))
		}
		if (pa.Action == ActSet || pa.Action == ActDrop || pa.Action == ActDeny || pa.Action == ActAllow || pa.Action == ActRename || pa.Action == ActCompactContext || pa.Action == ActCompactTools || pa.Action == ActIsolate) && strings.TrimSpace(pa.Match) == "" {
			return fmt.Errorf("patch %q of %s says %s of what? give it a match", pa.ID, p.ID, pa.Action)
		}
	}
	for k := range p.Isolate {
		if !known(isolates, k) {
			return fmt.Errorf("%q is nothing a preset may take over: one of %s", k, strings.Join(isolates, ", "))
		}
	}
	return nil
}

// Isoaltes are the session parts a preset may take over.
var isolates = []string{"compaction", "planMode", "terminals", "toolResultPruner", "timeContext"}

// Isolates are the session parts a preset may take over.
func Isolates() []string { return append([]string(nil), isolates...) }

func actionsOf(kind string) []string {
	switch kind {
	case KindPrompt:
		return []string{ActAppend, ActPrepend, ActSet, ActDrop}
	case KindContext:
		return []string{ActAllow, ActDeny, ActCompactContext}
	case KindTools:
		return []string{ActAllow, ActDeny, ActRename, ActCompactTools}
	}
	return nil
}

func known(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Sorted is presets in their cards' order: by Order, then by name.
func Sorted(ps []Preset) []Preset {
	out := append([]Preset(nil), ps...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Clone is a deep copy, so a saved edit never shares a slice with a
// built-in.
func (p Preset) Clone() Preset {
	c := p
	c.Patches = append([]Patch(nil), p.Patches...)
	if p.Isolate != nil {
		c.Isolate = make(map[string]bool, len(p.Isolate))
		for k, v := range p.Isolate {
			c.Isolate[k] = v
		}
	}
	return c
}

// JSON is a preset's own JSON, as the window and the CLI take it.
func (p Preset) JSON() []byte {
	b, _ := json.Marshal(p)
	return b
}
