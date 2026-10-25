package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/soundadam/facet/internal/launch"
	"github.com/soundadam/facet/internal/profile"
	"github.com/soundadam/facet/internal/secret"
)

func (app *App) newUICommand() *cobra.Command {
	return &cobra.Command{
		Use:     "ui",
		Short:   "Manage profiles in an interactive screen (the default on a terminal)",
		GroupID: groupProfiles,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.runUI(cmd.Context())
		},
	}
}

func (app *App) runUI(ctx context.Context) error {
	m := &uiModel{app: app, ctx: ctx, busy: map[string]string{}}
	_, err := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(app.In), tea.WithOutput(app.Out)).Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil
	}
	return err
}

// Browsers keep running after the screen closes: each one belongs to its
// own detached supervisor, not to this process.

type uiMode int

const (
	modeList uiMode = iota
	modeForm
	modeConfirm
)

type uiRow struct {
	p     *profile.Profile
	state *profile.RunState
	// password is the keychain status line, read on full reloads only:
	// the keychain is too slow to ask on every refresh tick.
	password string
}

type uiModel struct {
	app *App
	ctx context.Context

	rows    []uiRow
	loadErr error
	cursor  int
	width   int
	height  int

	mode    uiMode
	form    *uiForm
	confirm *uiConfirm

	// busy maps profile IDs to the action running for them.
	busy map[string]string
	// selectID moves the cursor to that profile when the next load lands.
	selectID  string
	status    string
	statusErr bool
}

type uiConfirm struct {
	prompt string
	run    func() (string, error)
	id     string
	verb   string
}

type (
	tickMsg   struct{}
	loadedMsg struct {
		rows []uiRow
		err  error
	}
	doneMsg struct {
		id     string
		msg    string
		err    error
		reload bool
	}
)

const refreshEvery = 2 * time.Second

func (m *uiModel) Init() tea.Cmd {
	return tea.Batch(m.load(), tick())
}

func tick() tea.Cmd {
	return tea.Tick(refreshEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *uiModel) load() tea.Cmd {
	store := m.app.Store
	return func() tea.Msg {
		profiles, err := store.List()
		rows := make([]uiRow, len(profiles))
		for i, p := range profiles {
			rows[i] = uiRow{p: p, password: passwordStatus(p)}
			rows[i].state, _ = store.RunState(p.ID)
		}
		return loadedMsg{rows: rows, err: err}
	}
}

func passwordStatus(p *profile.Profile) string {
	if !p.Proxy.Authenticated() {
		return ""
	}
	pw, err := secret.ProxyPassword(p.ID)
	switch {
	case err != nil:
		return "keychain: " + err.Error()
	case pw == "":
		return "no password stored"
	default:
		return "password in keychain, via local bridge"
	}
}

// do runs action off the UI goroutine and reports its outcome.
func (m *uiModel) do(p *profile.Profile, verb string, reload bool, action func() (string, error)) tea.Cmd {
	m.busy[p.ID] = verb
	m.setStatus(fmt.Sprintf("%s #%d %s…", verb, p.Serial, p.Name), false)
	return func() tea.Msg {
		msg, err := action()
		return doneMsg{id: p.ID, msg: msg, err: err, reload: reload}
	}
}

func (m *uiModel) setStatus(s string, isErr bool) {
	m.status, m.statusErr = s, isErr
}

func (m *uiModel) selected() *uiRow {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return &m.rows[m.cursor]
}

func (m *uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.form != nil {
			m.form.resize(m.width)
		}
		return m, nil
	case tickMsg:
		for i := range m.rows {
			m.rows[i].state, _ = m.app.Store.RunState(m.rows[i].p.ID)
		}
		return m, tick()
	case loadedMsg:
		keep := m.selectID
		if r := m.selected(); keep == "" && r != nil {
			keep = r.p.ID
		}
		m.selectID = ""
		m.rows, m.loadErr = msg.rows, msg.err
		m.cursor = min(m.cursor, max(len(m.rows)-1, 0))
		for i, r := range m.rows {
			if r.p.ID == keep {
				m.cursor = i
			}
		}
		return m, nil
	case doneMsg:
		delete(m.busy, msg.id)
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
		} else {
			m.setStatus(msg.msg, false)
		}
		for i := range m.rows {
			m.rows[i].state, _ = m.app.Store.RunState(m.rows[i].p.ID)
		}
		if msg.reload {
			return m, m.load()
		}
		return m, nil
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		switch m.mode {
		case modeForm:
			return m.updateForm(msg)
		case modeConfirm:
			return m.updateConfirm(msg)
		}
		return m.updateList(msg)
	}
	if m.mode == modeForm {
		return m, m.form.update(msg)
	}
	return m, nil
}

func (m *uiModel) updateList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		return m, tea.Quit
	case "up", "k":
		m.cursor = max(m.cursor-1, 0)
		return m, nil
	case "down", "j":
		m.cursor = min(m.cursor+1, max(len(m.rows)-1, 0))
		return m, nil
	case "home", "g":
		m.cursor = 0
		return m, nil
	case "end", "G":
		m.cursor = max(len(m.rows)-1, 0)
		return m, nil
	case "r":
		m.setStatus("", false)
		return m, m.load()
	case "n":
		m.form = newProfileForm(nil, "", m.width)
		m.mode = modeForm
		return m, m.form.focusCmd()
	}

	r := m.selected()
	if r == nil {
		return m, nil
	}
	p := r.p
	if verb, ok := m.busy[p.ID]; ok {
		m.setStatus(fmt.Sprintf("#%d %s: still %s", p.Serial, p.Name, verb), true)
		return m, nil
	}
	switch msg.String() {
	case "enter", "o":
		verb := "opening"
		if r.state != nil {
			verb = "opening a window in"
		}
		return m, m.do(p, verb, false, func() (string, error) { return m.app.openProfile(m.ctx, p, nil) })
	case "s":
		if r.state == nil {
			m.setStatus(fmt.Sprintf("#%d %s is not running", p.Serial, p.Name), false)
			return m, nil
		}
		return m, m.do(p, "stopping", false, func() (string, error) { return m.app.stopProfile(p) })
	case "e":
		m.form = newProfileForm(p, r.password, m.width)
		m.mode = modeForm
		return m, m.form.focusCmd()
	case "w":
		m.ask(p, "wiping", fmt.Sprintf("Wipe #%d %s? Cookies, logins and history go to the trash; the fingerprint stays.", p.Serial, p.Name), func() (string, error) {
			dest, err := m.app.Store.Wipe(p)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("wiped #%d %s; old data is in %s", p.Serial, p.Name, dest), nil
		})
	case "d", "x":
		m.ask(p, "deleting", fmt.Sprintf("Delete #%d %s? Its whole directory goes to the trash and its proxy password leaves the keychain.", p.Serial, p.Name), func() (string, error) {
			dest, err := m.app.Store.Remove(p)
			if err != nil {
				return "", err
			}
			msg := fmt.Sprintf("moved #%d %s to %s", p.Serial, p.Name, dest)
			if err := secret.DeleteProxyPassword(p.ID); err != nil {
				msg += "; proxy password not removed from the keychain: " + err.Error()
			}
			return msg, nil
		})
	}
	return m, nil
}

func (m *uiModel) ask(p *profile.Profile, verb, prompt string, run func() (string, error)) {
	if state, _ := m.app.Store.RunState(p.ID); state != nil {
		m.setStatus(fmt.Sprintf("#%d %s is running; stop it first (s)", p.Serial, p.Name), true)
		return
	}
	m.confirm = &uiConfirm{prompt: prompt, run: run, id: p.ID, verb: verb}
	m.mode = modeConfirm
}

func (m *uiModel) updateConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := m.confirm
	m.confirm, m.mode = nil, modeList
	if msg.String() != "y" {
		m.setStatus("cancelled", false)
		return m, nil
	}
	r := m.selected()
	if r == nil || r.p.ID != c.id {
		return m, nil
	}
	return m, m.do(r.p, c.verb, true, c.run)
}

func (m *uiModel) updateForm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	f := m.form
	switch msg.String() {
	case "esc":
		m.form, m.mode = nil, modeList
		return m, nil
	case "tab", "down":
		return m, f.move(1)
	case "shift+tab", "up":
		return m, f.move(-1)
	case "enter":
		if f.focus < len(f.fields)-1 {
			return m, f.move(1)
		}
		return m.submitForm()
	case "ctrl+s":
		return m.submitForm()
	}
	return m, f.update(msg)
}

func (m *uiModel) submitForm() (tea.Model, tea.Cmd) {
	p, err := m.app.saveForm(m.form)
	if err != nil {
		m.form.err = err.Error()
		return m, nil
	}
	verb := "updated"
	if m.form.edit == nil {
		verb = "created"
	}
	status := fmt.Sprintf("%s #%d %s", verb, p.Serial, p.Name)
	if state, _ := m.app.Store.RunState(p.ID); state != nil {
		status += "; it is running, so stop and open it for the change to apply"
	}
	m.setStatus(status, false)
	m.form, m.mode = nil, modeList
	m.selectID = p.ID
	return m, m.load()
}

// The profile form.

type uiField struct {
	key   string
	label string
	hint  string
	input textinput.Model
}

type uiForm struct {
	edit   *profile.Profile
	fields []uiField
	focus  int
	err    string
}

// commonZones are offered as completions in the timezone field.
var commonZones = []string{
	"UTC",
	"America/New_York", "America/Chicago", "America/Denver", "America/Los_Angeles",
	"America/Toronto", "America/Vancouver", "America/Mexico_City", "America/Sao_Paulo",
	"Europe/London", "Europe/Dublin", "Europe/Paris", "Europe/Berlin", "Europe/Amsterdam",
	"Europe/Madrid", "Europe/Rome", "Europe/Stockholm", "Europe/Warsaw", "Europe/Moscow",
	"Asia/Tokyo", "Asia/Seoul", "Asia/Shanghai", "Asia/Hong_Kong", "Asia/Taipei",
	"Asia/Singapore", "Asia/Bangkok", "Asia/Jakarta", "Asia/Kolkata", "Asia/Dubai",
	"Australia/Sydney", "Australia/Melbourne", "Pacific/Auckland",
}

var commonLanguages = []string{
	"en-US", "en-GB", "en-CA", "en-AU", "zh-CN", "zh-TW", "zh-HK", "ja-JP", "ko-KR",
	"de-DE", "fr-FR", "es-ES", "es-MX", "it-IT", "pt-BR", "ru-RU", "nl-NL", "pl-PL",
}

func newProfileForm(p *profile.Profile, passwordState string, width int) *uiForm {
	f := &uiForm{edit: p}
	add := func(key, label, hint, value string) *textinput.Model {
		in := textinput.New()
		in.Prompt = ""
		in.SetValue(value)
		in.KeyMap.AcceptSuggestion = key2("right")
		f.fields = append(f.fields, uiField{key: key, label: label, hint: hint, input: in})
		return &f.fields[len(f.fields)-1].input
	}
	if p == nil {
		p = &profile.Profile{Fingerprint: profile.Fingerprint{Platform: hostPlatform()}}
		add("name", "name", "one per account, e.g. shop-us", "")
		add("platform", "platform", "macos, windows or linux; fixed once created", p.Fingerprint.Platform).
			SetSuggestions([]string{profile.PlatformMacOS, profile.PlatformWindows, profile.PlatformLinux})
	} else {
		add("name", "name", "", p.Name)
	}
	proxy := ""
	if !p.Proxy.Direct() {
		proxy = p.Proxy.String()
	}
	add("proxy", "proxy", "socks5://user@host:port, http://… or empty for direct", proxy)
	pwHint := "only for a proxy with a user name; stored in the keychain"
	if passwordState != "" {
		pwHint = passwordState + "; empty keeps it"
	}
	pw := add("password", "password", pwHint, "")
	pw.EchoMode = textinput.EchoPassword
	add("timezone", "timezone", "IANA zone matching the proxy's exit, e.g. America/New_York; → completes", p.Locale.Timezone).
		SetSuggestions(commonZones)
	add("language", "language", "e.g. en-US; → completes", p.Locale.Language).
		SetSuggestions(commonLanguages)
	add("notes", "notes", "which account this is", p.Notes)
	add("urls", "start pages", "space-separated URLs opened on launch", strings.Join(p.StartURLs, " "))
	for i := range f.fields {
		f.fields[i].input.ShowSuggestions = true
	}
	f.resize(width)
	return f
}

func key2(keys ...string) key.Binding { return key.NewBinding(key.WithKeys(keys...)) }

func (f *uiForm) resize(width int) {
	w := max(width-labelWidth-6, 20)
	for i := range f.fields {
		f.fields[i].input.SetWidth(w)
	}
}

func (f *uiForm) focusCmd() tea.Cmd {
	for i := range f.fields {
		f.fields[i].input.Blur()
	}
	return f.fields[f.focus].input.Focus()
}

func (f *uiForm) move(delta int) tea.Cmd {
	f.focus = (f.focus + delta + len(f.fields)) % len(f.fields)
	return f.focusCmd()
}

func (f *uiForm) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	f.fields[f.focus].input, cmd = f.fields[f.focus].input.Update(msg)
	return cmd
}

func (f *uiForm) value(key string) string {
	for _, fl := range f.fields {
		if fl.key == key {
			return strings.TrimSpace(fl.input.Value())
		}
	}
	return ""
}

// saveForm validates the form like `new` and `set` validate their flags,
// then writes the profile and its proxy password.
func (app *App) saveForm(f *uiForm) (*profile.Profile, error) {
	name := f.value("name")
	var p *profile.Profile
	if f.edit == nil {
		var err error
		if p, err = profile.New(name, f.value("platform"), time.Now()); err != nil {
			return nil, err
		}
	} else {
		cp := *f.edit
		p = &cp
		if err := profile.ValidateName(name); err != nil {
			return nil, err
		}
		if !strings.EqualFold(name, p.Name) {
			if other, err := app.Store.Resolve(name); err == nil && other.ID != p.ID {
				return nil, fmt.Errorf("a profile named %q already exists (#%d)", other.Name, other.Serial)
			}
		}
		p.Name = name
	}

	tz, lang := f.value("timezone"), f.value("language")
	if err := checkTimezone(tz); err != nil {
		return nil, err
	}
	if err := checkLanguage(lang); err != nil {
		return nil, err
	}
	if lang != p.Locale.Language {
		// A hand-set Accept-Language belonged to the old language.
		p.Locale.AcceptLanguage = ""
	}
	p.Locale.Timezone, p.Locale.Language = tz, lang
	p.Notes = f.value("notes")
	p.StartURLs = strings.Fields(f.value("urls"))

	proxy, password, err := profile.ParseProxy(f.value("proxy"))
	if err != nil {
		return nil, err
	}
	if typed := f.value("password"); typed != "" {
		password = typed
	}
	// An empty password field keeps the stored one while the user stays.
	keep := f.edit != nil && password == "" && proxy.Authenticated() && f.edit.Proxy.Username == proxy.Username
	p.Proxy = proxy

	if f.edit == nil {
		if err := app.Store.Create(p); err != nil {
			return nil, err
		}
		if password == "" {
			return p, nil
		}
	} else if err := app.Store.Save(p); err != nil {
		return nil, err
	}
	return p, app.storePassword(p, password, keep)
}

// Rendering.

const labelWidth = 12

var (
	styleTitle     = lipgloss.NewStyle().Bold(true)
	styleDim       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleCursor    = lipgloss.NewStyle().Reverse(true)
	styleRunning   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleWarn      = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleErr       = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleLabel     = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Width(labelWidth)
	styleKey       = lipgloss.NewStyle().Bold(true)
	styleFormFocus = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true).Width(labelWidth)
)

func (m *uiModel) View() tea.View {
	var b strings.Builder
	switch m.mode {
	case modeForm:
		m.viewForm(&b)
	default:
		m.viewList(&b)
	}
	v := tea.NewView(b.String())
	v.AltScreen = true
	v.WindowTitle = "facet"
	return v
}

func (m *uiModel) viewList(b *strings.Builder) {
	running := 0
	for _, r := range m.rows {
		if r.state != nil {
			running++
		}
	}
	fmt.Fprintf(b, "%s  %s\n\n", styleTitle.Render("facet"), styleDim.Render(fmt.Sprintf("%d profiles · %d running · %s", len(m.rows), running, m.app.Store.Root)))

	if len(m.rows) == 0 {
		b.WriteString("  No profiles yet. Press n to create one.\n")
	} else {
		width := max(m.width, 60)
		nameW := 18
		proxyW := max(min(width-nameW-46, 40), 12)
		header := fmt.Sprintf("  %-4s%s  %s  %s  %s", "#", pad("NAME", nameW), pad("PROXY", proxyW), pad("TIMEZONE", 20), "STATE")
		b.WriteString(styleDim.Render(header) + "\n")
		for i, r := range m.visibleRows() {
			idx := i + m.offset()
			state := ""
			switch {
			case m.busy[r.p.ID] != "":
				state = styleWarn.Render(m.busy[r.p.ID] + "…")
			case r.state != nil && r.state.Orphaned() && r.state.Bridge != "":
				state = styleErr.Render("running, bridge down")
			case r.state != nil:
				state = styleRunning.Render("● running")
			}
			line := fmt.Sprintf("%-4s%s  %s  %s  ", strconv.Itoa(r.p.Serial), pad(r.p.Name, nameW), pad(r.p.Proxy.String(), proxyW), pad(dash(r.p.Locale.Timezone), 20))
			if idx == m.cursor {
				line = styleCursor.Render("▸ " + line)
			} else {
				line = "  " + line
			}
			b.WriteString(line + state + "\n")
		}
		if r := m.selected(); r != nil {
			b.WriteString("\n")
			m.viewDetail(b, r)
		}
	}
	if m.loadErr != nil {
		b.WriteString("\n" + styleErr.Render(m.loadErr.Error()) + "\n")
	}

	b.WriteString("\n")
	if m.mode == modeConfirm {
		b.WriteString(styleWarn.Render(m.confirm.prompt) + "  " + styleKey.Render("y") + "/N\n")
		return
	}
	if m.status != "" {
		st := styleDim
		if m.statusErr {
			st = styleErr
		}
		b.WriteString(st.Render(ansi.Truncate(m.status, max(m.width, 40), "…")) + "\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString(help("enter", "open", "s", "stop", "n", "new", "e", "edit", "w", "wipe data", "d", "delete", "r", "reload", "q", "quit"))
}

// listRows is how many profile lines fit beside the header, detail panel
// and footer.
func (m *uiModel) listRows() int {
	if m.height == 0 {
		return len(m.rows)
	}
	return max(m.height-22, 3)
}

func (m *uiModel) offset() int {
	n := m.listRows()
	if m.cursor < n {
		return 0
	}
	return m.cursor - n + 1
}

func (m *uiModel) visibleRows() []uiRow {
	start := m.offset()
	return m.rows[start:min(start+m.listRows(), len(m.rows))]
}

func (m *uiModel) viewDetail(b *strings.Builder, r *uiRow) {
	p := r.p
	fp, loc := p.Fingerprint, p.Locale
	row := func(k, v string) {
		if v != "" {
			b.WriteString("  " + styleLabel.Render(k) + v + "\n")
		}
	}
	row("platform", fmt.Sprintf("%s · seed %d · %d cores", strings.TrimSpace(fp.Platform+" "+fp.PlatformVersion), fp.Seed, fp.HardwareConcurrency))
	if fp.Brand != "" {
		row("brand", strings.TrimSpace(fp.Brand+" "+fp.BrandVersion))
	}
	proxy := p.Proxy.String()
	if r.password != "" {
		proxy += styleDim.Render("  (" + r.password + ")")
	}
	row("proxy", proxy)
	if !p.Proxy.Direct() && loc.Timezone == "" {
		row("timezone", styleWarn.Render("unset: the browser reports this machine's zone through the proxy"))
	} else {
		row("timezone", dash(loc.Timezone))
	}
	accept := loc.AcceptLanguage
	if accept == "" && loc.Language != "" {
		accept = launch.AcceptLanguage(loc.Language)
	}
	lang := dash(loc.Language)
	if accept != "" {
		lang += styleDim.Render("  accept " + accept)
	}
	row("language", lang)
	row("notes", p.Notes)
	row("start pages", strings.Join(p.StartURLs, " "))
	row("data", m.app.Store.Dir(p.ID))
	if st := r.state; st != nil {
		s := fmt.Sprintf("%s since %s, browser pid %d", runningText(st), st.Started.Local().Format("15:04"), st.BrowserPID)
		if st.Bridge != "" {
			s += ", bridge " + st.Bridge
		}
		row("state", s)
	}
}

func (m *uiModel) viewForm(b *strings.Builder) {
	f := m.form
	title := "New profile"
	if f.edit != nil {
		title = fmt.Sprintf("Edit #%d %s", f.edit.Serial, f.edit.Name)
	}
	b.WriteString(styleTitle.Render(title))
	if f.edit != nil {
		b.WriteString(styleDim.Render(fmt.Sprintf("  %s · seed %d (fixed)", f.edit.Fingerprint.Platform, f.edit.Fingerprint.Seed)))
	}
	b.WriteString("\n\n")
	for i, fl := range f.fields {
		label := styleLabel.Render(fl.label)
		if i == f.focus {
			label = styleFormFocus.Render(fl.label)
		}
		b.WriteString("  " + label + fl.input.View() + "\n")
		if i == f.focus && fl.hint != "" {
			b.WriteString("  " + strings.Repeat(" ", labelWidth) + styleDim.Render(fl.hint) + "\n")
		}
	}
	b.WriteString("\n")
	if f.err != "" {
		b.WriteString(styleErr.Render(f.err) + "\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString(help("tab/↓", "next", "shift+tab/↑", "back", "→", "complete", "enter", "next / save on last", "ctrl+s", "save", "esc", "cancel"))
}

func help(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, styleKey.Render(pairs[i])+" "+styleDim.Render(pairs[i+1]))
	}
	return strings.Join(parts, styleDim.Render(" · "))
}

// pad fits s into w terminal cells, truncating with an ellipsis.
func pad(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(w-lipgloss.Width(s), 0))
}
