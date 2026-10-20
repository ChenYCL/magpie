package presets

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/tidwall/gjson"
)

// Apply edits an agent's request body with a preset, in the agent's own
// API (proto: "chat", "anthropic", "responses" or "gemini"), and returns
// the body the vendor is to get and what the preset did. A body no patch
// touched is returned as the very bytes it came in as: a preset that has
// nothing to say leaves a request byte-identical, which is what keeps it
// out of bug reports.
func Apply(p Preset, proto string, body []byte) ([]byte, Stats) {
	st := Stats{BytesIn: len(body), BytesOut: len(body)}
	if len(p.Patches) == 0 || len(body) == 0 || proto == "" {
		return body, st
	}
	doc, ok := decode(body)
	if !ok {
		return body, st
	}
	sys := systemOf(proto, doc)
	tools := toolsOf(proto, doc)

	// one pass per kind, the files and tools before the prompt's own text:
	// an instruction file's chunk runs to the next file or to the end of
	// the prompt, so a patch that appended to the prompt first would have
	// its text taken for part of the last file. The counters still read as
	// the window lists them (prompt, context, tools).
	for _, kind := range applyOrder {
		done := map[string]bool{} // what this kind already acted on
		for _, pa := range p.Patches {
			if pa.Kind != kind {
				continue
			}
			if pa.Disabled {
				st.add(kind, false, skipNote(pa, "off"))
				continue
			}
			applied, note := applyPatch(pa, proto, &sys, tools)
			if applied && pa.Match != "" {
				if done[strings.ToLower(pa.Match)] {
					applied, note = false, "already patched"
				}
				done[strings.ToLower(pa.Match)] = true
			}
			st.add(kind, applied, skipNote(pa, note))
		}
	}
	st.Settle()
	if st.Applied == 0 {
		return body, st
	}
	// the system text and the tools are written back only where they were
	if sys.dirty {
		sys.put(doc)
	}
	if tools.dirty {
		tools.put(doc)
	}
	// the prompt's own text, not JSON-escaped: a "<skills>" section the
	// model reads is the same bytes whether it went through here or not
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return body, st
	}
	out := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	st.BytesOut = len(out)
	return out, st
}

// applyOrder is the kinds as they are applied: what is inside the prompt
// (the instruction files), then the tools, then the prompt's own text.
var applyOrder = []string{KindContext, KindTools, KindPrompt}

func skipNote(pa Patch, why string) string {
	if why == "" {
		return ""
	}
	name := pa.ID
	if name == "" {
		name = pa.Action
	}
	return name + ": " + why
}

func decode(body []byte) (map[string]any, bool) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil || doc == nil {
		return nil, false
	}
	return doc, true
}

// ---- the system prompt ------------------------------------------------------

// system is the request's system text, wherever the API keeps it, and how
// to write it back.
type system struct {
	proto string
	doc   map[string]any
	// text is the prompt as it stands, "" when the API keeps none.
	text string
	// where is the field it lives in: "messages" (a chat system message),
	// "system" (Anthropic), "instructions" (Responses), "systemInstruction"
	// (Gemini); "" when there is none.
	where string
	// at is the index in messages, for a chat system message.
	at    int
	dirty bool
}

func systemOf(proto string, doc map[string]any) system {
	s := system{proto: proto, doc: doc, at: -1}
	switch proto {
	case "chat":
		msgs, _ := doc["messages"].([]any)
		for i, m := range msgs {
			mm, ok := m.(map[string]any)
			if !ok {
				continue
			}
			if r, _ := mm["role"].(string); r == "system" || r == "developer" {
				s.where, s.at = "messages", i
				s.text = textOf(mm["content"])
				// a developer message is left as it is: OpenAI's own
				// shape, and not every backend takes a system one
				if r == "developer" {
					s.where = ""
				}
				return s
			}
		}
	case "anthropic":
		if v, ok := doc["system"]; ok {
			s.where, s.text = "system", textOf(v)
		}
		return s
	case "responses":
		if v, ok := doc["instructions"]; ok {
			s.where, s.text = "instructions", textOf(v)
		}
		return s
	case "gemini":
		if v, ok := doc["systemInstruction"]; ok {
			s.where, s.text = "systemInstruction", textOf(v)
		}
		return s
	}
	return s
}

// put writes the edited text back where it came from, in the shape that
// field had: a string stays a string, a block list keeps its blocks and
// gets the text in its first text block.
func (s system) put(doc map[string]any) {
	switch s.where {
	case "messages":
		msgs, _ := doc["messages"].([]any)
		if s.at < 0 || s.at >= len(msgs) {
			return
		}
		mm, _ := msgs[s.at].(map[string]any)
		if mm == nil {
			return
		}
		mm["content"] = rewriteContent(mm["content"], s.text)
	case "system":
		doc["system"] = rewriteContent(doc["system"], s.text)
	case "instructions":
		if _, ok := doc["instructions"].(string); ok {
			doc["instructions"] = s.text
			return
		}
		doc["instructions"] = rewriteContent(doc["instructions"], s.text)
	case "systemInstruction":
		si, _ := doc["systemInstruction"].(map[string]any)
		if si == nil {
			doc["systemInstruction"] = map[string]any{"parts": []any{map[string]any{"text": s.text}}}
			return
		}
		si["parts"] = []any{map[string]any{"text": s.text}}
		doc["systemInstruction"] = si
	}
}

// rewriteContent is content with its text replaced, keeping the shape it
// has: a string, or a block list whose first text block takes the text.
func rewriteContent(v any, text string) any {
	switch x := v.(type) {
	case string:
		return text
	case []any:
		done := false
		for _, b := range x {
			bm, ok := b.(map[string]any)
			if !ok {
				continue
			}
			if _, ok := bm["text"]; ok && !done {
				bm["text"] = text
				done = true
			}
		}
		if !done {
			return append([]any{map[string]any{"type": "text", "text": text}}, x...)
		}
		return x
	case map[string]any:
		if _, ok := x["text"]; ok {
			x["text"] = text
		}
		return x
	}
	return text
}

// textOf is a content field's text: a string as it is, a block list's
// text blocks run together, a Gemini parts object's texts.
func textOf(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var b strings.Builder
		for _, e := range x {
			switch e := e.(type) {
			case string:
				b.WriteString(e)
			case map[string]any:
				if t, ok := e["text"].(string); ok {
					b.WriteString(t)
				}
			}
		}
		return b.String()
	case map[string]any:
		if parts, ok := x["parts"].([]any); ok {
			return textOf(parts)
		}
		if t, ok := x["text"].(string); ok {
			return t
		}
	}
	return ""
}

// ---- tools ------------------------------------------------------------------

// toolList is the request's tools, wherever the API keeps them.
type toolList struct {
	proto string
	doc   map[string]any
	// list is the tools' raw entries, in their order.
	list  []any
	dirty bool
}

func toolsOf(proto string, doc map[string]any) *toolList {
	t := &toolList{proto: proto, doc: doc}
	if v, ok := doc["tools"].([]any); ok {
		t.list = v
	}
	return t
}

// name is a tool entry's name and description, however the API spells them.
func toolName(e any) (name, desc string) {
	m, ok := e.(map[string]any)
	if !ok {
		return "", ""
	}
	if n, ok := m["name"].(string); ok {
		return n, str(m["description"])
	}
	if f, ok := m["function"].(map[string]any); ok {
		return str(f["name"]), str(f["description"])
	}
	return "", ""
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func (t *toolList) put(doc map[string]any) {
	doc["tools"] = t.list
}

// setName writes a tool's new name in the shape the API uses.
func setName(e any, name string) {
	m, ok := e.(map[string]any)
	if !ok {
		return
	}
	if _, ok := m["name"]; ok {
		m["name"] = name
		return
	}
	if f, ok := m["function"].(map[string]any); ok {
		f["name"] = name
	}
}

// compactTool keeps a tool's names and drops what only costs tokens: its
// description, and a parameter schema's descriptions.
func compactTool(e any) bool {
	m, ok := e.(map[string]any)
	if !ok {
		return false
	}
	changed := false
	if _, ok := m["description"]; ok {
		delete(m, "description")
		changed = true
	}
	target := m
	if f, ok := m["function"].(map[string]any); ok {
		target = f
	}
	if d, ok := target["description"]; ok && d != "" {
		delete(target, "description")
		changed = true
	}
	for _, k := range []string{"parameters", "input_schema"} {
		if schema, ok := target[k].(map[string]any); ok {
			if stripSchema(schema) {
				changed = true
			}
		}
	}
	return changed
}

// stripSchema takes the prose out of a JSON schema, keeping every name,
// type and requirement: what a model needs to call the tool.
func stripSchema(schema map[string]any) bool {
	changed := false
	for k, v := range schema {
		switch k {
		case "description", "$comment", "examples", "example":
			delete(schema, k)
			changed = true
			continue
		}
		switch x := v.(type) {
		case map[string]any:
			if stripSchema(x) {
				changed = true
			}
		case []any:
			for _, e := range x {
				if m, ok := e.(map[string]any); ok && stripSchema(m) {
					changed = true
				}
			}
		}
	}
	return changed
}

// ---- applying one patch -----------------------------------------------------

// applyPatch does one patch and says whether it landed and, when it didn't,
// why in a word.
func applyPatch(pa Patch, proto string, sys *system, tools *toolList) (bool, string) {
	switch pa.Kind {
	case KindPrompt:
		return applyPrompt(pa, sys)
	case KindContext:
		return applyContext(pa, sys)
	case KindTools:
		return applyTools(pa, tools)
	}
	return false, "no such kind"
}

func applyPrompt(pa Patch, sys *system) (bool, string) {
	if sys.where == "" {
		switch pa.Action {
		case ActAppend, ActPrepend:
			return false, "this API keeps no system prompt to " + pa.Action + " to"
		}
		return false, "this API keeps no system prompt"
	}
	switch pa.Action {
	case ActAppend:
		sys.text = strings.TrimRight(sys.text, "\n") + "\n" + pa.Value
		sys.dirty = true
		return true, ""
	case ActPrepend:
		sys.text = pa.Value + "\n" + sys.text
		sys.dirty = true
		return true, ""
	case ActDrop, ActSet:
		tag := strings.TrimSpace(strings.Trim(pa.Match, "<>"))
		before, _, after, ok := cutSection(sys.text, tag)
		if !ok {
			return false, "no section <" + tag + ">"
		}
		if pa.Action == ActDrop {
			sys.text = before + after
		} else {
			// the section keeps its tags and takes the patch's text
			sys.text = before + "<" + tag + ">" + pa.Value + "</" + tag + ">" + after
		}
		sys.dirty = true
		return true, ""
	}
	return false, "nothing to do"
}

// cutSection takes the section a lead tag opens out of a text: what comes
// before it, what is inside it, and what follows its closing tag.
func cutSection(text, tag string) (before, inner, after string, ok bool) {
	tag = strings.TrimSpace(strings.Trim(tag, "<>"))
	if tag == "" {
		return "", "", "", false
	}
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	i := lineStart(text, open)
	if i < 0 {
		return "", "", "", false
	}
	j := strings.Index(text[i:], close)
	if j < 0 {
		return "", "", "", false
	}
	bodyStart := i + len(open)
	return text[:i], text[bodyStart : i+j], text[i+j+len(close):], true
}

// lineStart is where a tag opening a section is: at the start of the text
// or of a line, as the agents write them (the gateway's leadTag).
func lineStart(text, open string) int {
	for i := 0; ; {
		j := strings.Index(text[i:], open)
		if j < 0 {
			return -1
		}
		at := i + j
		if at == 0 || text[at-1] == '\n' || onlySpaces(text[lineStartOf(text, at):at]) {
			return at
		}
		i = at + len(open)
	}
}

func lineStartOf(text string, at int) int {
	i := strings.LastIndexByte(text[:at], '\n')
	return i + 1
}

func onlySpaces(s string) bool { return strings.TrimSpace(s) == "" }

// The instruction files, as the gateway reads them (internal/gateway's
// prompt.go): Claude Code's "Contents of <path> (<desc>):", OpenCode's
// "Instructions from: <path>" and Codex's "# <name>.md instructions for
// <path>". Kept here as the same patterns so a context patch names what
// the gateway counts as memory.
var (
	contentsOf       = regexp.MustCompile(`(?m)^Contents of (\S.*?) \(([^()\n]*(?:\([^()\n]*\)[^()\n]*)*)\):[ \t]*$`)
	instructionsFrom = regexp.MustCompile(`(?m)^Instructions from: (\S.*?)[ \t]*$`)
	agentsFor        = regexp.MustCompile(`^# (\S+\.md) instructions for (\S.*?)[ \t]*\n`)
)

// compactedNote stands in the prompt where an instruction file's text was,
// so the model still knows the file was read.
const compactedNote = "(compacted by the preset: this file's contents are left out of the prompt)"

// contextFile is one instruction file in a text: where it starts, where its
// body begins, and the path it names.
type contextFile struct {
	at, body int
	path     string
	desc     string
}

// contextFiles are the instruction files a text carries, in their order.
func contextFiles(text string) []contextFile {
	var out []contextFile
	for _, m := range contentsOf.FindAllStringSubmatchIndex(text, -1) {
		out = append(out, contextFile{at: m[0], body: m[1], path: text[m[2]:m[3]], desc: text[m[4]:m[5]]})
	}
	for _, m := range instructionsFrom.FindAllStringSubmatchIndex(text, -1) {
		out = append(out, contextFile{at: m[0], body: m[1], path: text[m[2]:m[3]]})
	}
	if m := agentsFor.FindStringSubmatchIndex(text); m != nil {
		out = append(out, contextFile{at: m[0], body: m[0], path: text[m[2]:m[3]]})
	}
	sortFiles(out)
	return out
}

func sortFiles(f []contextFile) {
	for i := 1; i < len(f); i++ {
		for j := i; j > 0 && f[j].at < f[j-1].at; j-- {
			f[j], f[j-1] = f[j-1], f[j]
		}
	}
}

// applyContext keeps, drops or compacts the instruction files in the system
// prompt the request carries.
func applyContext(pa Patch, sys *system) (bool, string) {
	if sys.where == "" {
		return false, "this API keeps no system prompt to patch"
	}
	files := contextFiles(sys.text)
	if len(files) == 0 {
		return false, "no instruction files in the prompt"
	}
	switch pa.Action {
	case ActDeny, ActCompactContext:
		var b strings.Builder
		at := 0
		hit := false
		for i, f := range files {
			if !matchPath(pa.Match, f.path) {
				continue
			}
			hit = true
			end := len(sys.text)
			if i+1 < len(files) {
				end = files[i+1].at
			}
			// what the agent put after the file — a reminder's closing tag
			// and what follows it — isn't the file's and stays
			tail := ""
			if j := strings.Index(sys.text[f.at:end], "</system-reminder>"); j >= 0 {
				tail = sys.text[f.at+j : end]
				end = f.at + j
			}
			b.WriteString(sys.text[at:f.body])
			if pa.Action == ActCompactContext {
				// the path, and the fact that it was read, stay; the text goes
				b.WriteString(compactedNote)
			}
			b.WriteString(tail)
			at = end
		}
		if !hit {
			return false, "no instruction file matches " + pa.Match
		}
		b.WriteString(sys.text[at:])
		sys.text = b.String()
		sys.dirty = true
		return true, ""
	case ActAllow:
		var b strings.Builder
		at := 0
		hit := false
		for i, f := range files {
			end := len(sys.text)
			if i+1 < len(files) {
				end = files[i+1].at
			}
			if matchPath(pa.Match, f.path) {
				hit = true
				b.WriteString(sys.text[at:end])
			}
			at = end
		}
		if !hit {
			return false, "no instruction file matches " + pa.Match
		}
		sys.text = b.String()
		sys.dirty = true
		return true, ""
	}
	return false, "nothing to do"
}

// matchPath says a file's path is what a patch names: the whole path, its
// base name (CLAUDE.md), a prefix of it, or a pattern with * for any run of
// characters (which crosses / too, so **/*.md reads as */*.md).
func matchPath(pattern, path string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	p := strings.ReplaceAll(path, "\\", "/")
	for _, pat := range strings.Split(pattern, "|") {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			continue
		}
		if pat == p || pat == base(p) {
			return true
		}
		if !strings.Contains(pat, "*") {
			if strings.HasSuffix(p, "/"+pat) || strings.HasSuffix(p, pat) {
				return true
			}
			continue
		}
		if globMatch(pat, p) || globMatch(pat, base(p)) {
			return true
		}
	}
	return false
}

func base(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// globMatch matches a pattern with * (any run, / included) and ? (one
// character) against s.
func globMatch(pat, s string) bool {
	// classic backtracking match; the patterns are short
	pi, si, star, mark := 0, 0, -1, 0
	for si < len(s) {
		switch {
		case pi < len(pat) && (pat[pi] == '?' || pat[pi] == s[si]):
			pi++
			si++
		case pi < len(pat) && pat[pi] == '*':
			star, mark = pi, si
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			si = mark
		default:
			return false
		}
	}
	for pi < len(pat) && pat[pi] == '*' {
		pi++
	}
	return pi == len(pat)
}

// applyTools allows, denies, renames or compacts the tools the request
// offers.
func applyTools(pa Patch, tools *toolList) (bool, string) {
	if tools == nil || len(tools.list) == 0 {
		return false, "the request offers no tools"
	}
	switch pa.Action {
	case ActDeny:
		kept := tools.list[:0:0]
		hit := false
		for _, e := range tools.list {
			n, _ := toolName(e)
			if matchTool(pa.Match, n) {
				hit = true
				continue
			}
			kept = append(kept, e)
		}
		if !hit {
			return false, "no tool matches " + pa.Match
		}
		tools.list = kept
		tools.dirty = true
		return true, ""
	case ActAllow:
		kept := tools.list[:0:0]
		hit := false
		for _, e := range tools.list {
			n, _ := toolName(e)
			if matchTool(pa.Match, n) {
				hit = true
				kept = append(kept, e)
			}
		}
		if !hit {
			return false, "no tool matches " + pa.Match
		}
		tools.list = kept
		tools.dirty = true
		return true, ""
	case ActRename:
		hit := false
		for _, e := range tools.list {
			n, _ := toolName(e)
			if !matchTool(pa.Match, n) {
				continue
			}
			hit = true
			if n != pa.Value {
				setName(e, pa.Value)
				tools.dirty = true
			}
		}
		if !hit {
			return false, "no tool matches " + pa.Match
		}
		return true, ""
	case ActCompactTools:
		hit := false
		for _, e := range tools.list {
			n, _ := toolName(e)
			if !matchTool(pa.Match, n) {
				continue
			}
			hit = true
			if compactTool(e) {
				tools.dirty = true
			}
		}
		if !hit {
			return false, "no tool matches " + pa.Match
		}
		return true, ""
	}
	return false, "nothing to do"
}

// matchTool says a tool's name is what a patch names: the whole name, or a
// pattern with * (mcp__*, Read|Write is written as two matches).
func matchTool(pattern, name string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || name == "" {
		return false
	}
	for _, pat := range strings.Split(pattern, "|") {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			continue
		}
		if strings.EqualFold(pat, name) {
			return true
		}
		if strings.Contains(pat, "*") && globMatch(strings.ToLower(pat), strings.ToLower(name)) {
			return true
		}
	}
	return false
}

// Counted is a body's tools and their sizes, for the window's card: how
// many tools a request offers, and their bytes.
func Counted(proto string, body []byte) (tools int, bytes int) {
	if proto == "" || len(body) == 0 {
		return 0, 0
	}
	raw := gjson.GetBytes(body, "tools")
	if !raw.IsArray() {
		return 0, 0
	}
	n := 0
	for _, t := range raw.Array() {
		n++
		bytes += len(t.Raw)
	}
	return n, bytes
}
