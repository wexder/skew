package main

import (
	"fmt"
	"math"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	maxStoredLines  = 2000
	minPanelWidth   = 36
	minPanelHeight  = 8
	colorBackground = "#080808"
	colorText       = "#FFFFFF"
	colorMuted      = "#AAAAAA"
	colorLine       = "#303030"
	colorAccent     = "#39FF82"
)

type serviceState struct {
	status   string
	runID    uint64
	exitCode int
	lines    []string
	scroll   int
}

func (s *serviceState) appendLine(line string) {
	following := s.scroll == 0
	s.lines = append(s.lines, line)
	if len(s.lines) > maxStoredLines {
		s.lines = append([]string(nil), s.lines[len(s.lines)-maxStoredLines:]...)
	}
	if following {
		s.scroll = 0
	} else {
		s.scroll = min(len(s.lines)-1, s.scroll+1)
	}
}

type model struct {
	services       []Service
	state          []serviceState
	super          *supervisor
	panels         []int // Visible panel slots, each containing a service index.
	width          int
	height         int
	focusedPanel   int
	selected       int
	listOffset     int
	panelLimit     int // Zero means use the automatic panel count.
	sidebarFocused bool
	assigningPanel bool
	quitting       bool
	configPath     string
	only           string
}

type processMsg processEvent
type shutdownCompleteMsg struct{}

func newModel(services []Service, configPath, only string) model {
	state := make([]serviceState, len(services))
	for i := range state {
		state[i].status = "starting"
	}
	m := model{
		services:   services,
		state:      state,
		super:      newSupervisor(services),
		configPath: configPath,
		only:       only,
		width:      100,
		height:     30,
	}
	m.resizePanels()
	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.startAllCmd(), m.waitForEventCmd())
}

func (m model) startAllCmd() tea.Cmd {
	return func() tea.Msg {
		m.super.startWatching()
		m.super.startConfigWatching(m.configPath, m.only)
		for i := range m.services {
			m.super.start(i)
		}
		return nil
	}
}

func (m model) waitForEventCmd() tea.Cmd {
	return func() tea.Msg { return processMsg(<-m.super.events) }
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizePanels()
		m.selectService(m.selected)
	case processMsg:
		event := processEvent(msg)
		if event.kind == "config-error" {
			m.state[m.selected].appendLine("[config reload error: " + event.err + "]")
			return m, m.waitForEventCmd()
		}
		if event.kind == "config-reload" {
			cmd := m.applyServices(event.services)
			return m, tea.Batch(cmd, m.waitForEventCmd())
		}
		index := event.index
		if event.service != "" {
			index = -1
			for i, service := range m.services {
				if service.Name == event.service {
					index = i
					break
				}
			}
		}
		if index >= 0 && index < len(m.state) {
			state := &m.state[index]
			if event.kind == "watch-error" {
				state.appendLine("[watch error: " + event.err + "]")
				return m, m.waitForEventCmd()
			}
			if event.kind == "watch" {
				state.appendLine("[watch: files changed (" + event.line + "), restarting]")
				if state.status == "running" || state.status == "failed" || state.status == "exited" {
					state.status = "starting"
					return m, tea.Batch(m.restartCmd(index), m.waitForEventCmd())
				}
				return m, m.waitForEventCmd()
			}
			if event.kind == "started" {
				if event.runID >= state.runID {
					state.runID = event.runID
					state.status = "running"
					state.exitCode = 0
				}
			} else if event.kind == "line" && event.runID == state.runID {
				state.appendLine(event.line)
			} else if event.runID == state.runID {
				switch event.kind {
				case "stopped":
					state.status = "stopped"
					state.exitCode = event.exitCode
				case "failed":
					state.status = "failed"
					state.exitCode = event.exitCode
					if event.err != "" {
						state.appendLine("[" + event.err + "]")
					}
				case "exited":
					state.status = "exited"
					state.exitCode = event.exitCode
				}
			}
		}
		return m, m.waitForEventCmd()
	case shutdownCompleteMsg:
		return m, tea.Quit
	case tea.KeyPressMsg:
		if m.quitting {
			return m, nil
		}
		key := msg.String()
		if key == "ctrl+c" || (!m.assigningPanel && (key == "q" || key == "esc")) {
			m.quitting = true
			for i := range m.state {
				if m.state[i].status == "running" || m.state[i].status == "starting" {
					m.state[i].status = "stopping"
				}
			}
			return m, func() tea.Msg {
				m.super.stopAll()
				return shutdownCompleteMsg{}
			}
		}
		if m.assigningPanel {
			if key == "esc" {
				m.assigningPanel = false
				return m, nil
			}
			switch key {
			case "left":
				m.focusedPanel = max(0, m.focusedPanel-1)
				return m, nil
			case "right":
				m.focusedPanel = min(len(m.panels)-1, m.focusedPanel+1)
				return m, nil
			case " ":
				m.assignSelectedTo(m.focusedPanel)
				return m, nil
			}
			if panel, ok := panelIndexForKey(key); ok && panel < len(m.panels) {
				m.assignSelectedTo(panel)
			}
			return m, nil
		}
		switch key {
		case "tab":
			m.sidebarFocused = !m.sidebarFocused
			if m.sidebarFocused && len(m.panels) > 0 {
				m.selectService(m.panels[m.focusedPanel])
			}
		case "left", "h":
			if !m.sidebarFocused {
				m.focusedPanel = max(0, m.focusedPanel-1)
			}
		case "right", "l":
			if !m.sidebarFocused {
				m.focusedPanel = min(len(m.panels)-1, m.focusedPanel+1)
			}
		case "up", "k":
			if m.sidebarFocused {
				m.selectService(max(0, m.selected-1))
			} else {
				m.scrollFocused(1)
			}
		case "down", "j":
			if m.sidebarFocused {
				m.selectService(min(len(m.services)-1, m.selected+1))
			} else {
				m.scrollFocused(-1)
			}
		case "enter":
			if m.sidebarFocused {
				if len(m.panels) == 1 {
					m.assignSelectedTo(0)
				} else {
					m.assigningPanel = true
				}
			}
		case "s":
			index := m.activeService()
			if m.state[index].status == "running" || m.state[index].status == "starting" {
				m.state[index].status = "stopping"
				return m, m.stopCmd(index)
			}
			if m.state[index].status == "stopped" || m.state[index].status == "exited" || m.state[index].status == "failed" {
				m.state[index].status = "starting"
				return m, m.startCmd(index)
			}
		case "r":
			index := m.activeService()
			m.state[index].status = "starting"
			return m, m.restartCmd(index)
		case "+":
			m.adjustPanelCount(1)
		case "-":
			m.adjustPanelCount(-1)
		case "0":
			m.panelLimit = 0
			m.resizePanels()
		case "c":
			state := m.activeState()
			state.lines = nil
			state.scroll = 0
		}
	}
	return m, nil
}

func (m *model) selectService(index int) {
	m.selected = max(0, min(len(m.services)-1, index))
	_, _, _, _, bodyHeight := m.layout()
	visible := max(1, bodyHeight-3)
	if m.selected < m.listOffset {
		m.listOffset = m.selected
	}
	if m.selected >= m.listOffset+visible {
		m.listOffset = m.selected - visible + 1
	}
}

func (m *model) resizePanels() {
	_, _, _, capacity, _ := m.layout()
	capacity = max(1, min(len(m.services), capacity))
	if m.panelLimit > 0 {
		m.panelLimit = min(m.panelLimit, capacity)
		capacity = min(capacity, m.panelLimit)
	}
	if len(m.panels) > capacity {
		m.panels = m.panels[:capacity]
	}
	assigned := make(map[int]bool, len(m.panels))
	for _, index := range m.panels {
		assigned[index] = true
	}
	for i := range m.services {
		if len(m.panels) == capacity {
			break
		}
		if !assigned[i] {
			m.panels = append(m.panels, i)
			assigned[i] = true
		}
	}
	m.focusedPanel = max(0, min(len(m.panels)-1, m.focusedPanel))
}

func (m *model) applyServices(services []Service) tea.Cmd {
	oldServices, oldState := m.services, m.state
	statesByName := make(map[string]serviceState, len(oldServices))
	shouldRunByName := make(map[string]bool, len(oldServices))
	for i, service := range oldServices {
		statesByName[service.Name] = oldState[i]
		shouldRunByName[service.Name] = oldState[i].status != "stopped" && oldState[i].status != "stopping"
	}
	oldSelected := ""
	if m.selected >= 0 && m.selected < len(oldServices) {
		oldSelected = oldServices[m.selected].Name
	}
	panelNames := make([]string, len(m.panels))
	for i, index := range m.panels {
		if index >= 0 && index < len(oldServices) {
			panelNames[i] = oldServices[index].Name
		}
	}

	newState := make([]serviceState, len(services))
	shouldRun := make([]bool, len(services))
	indexes := make(map[string]int, len(services))
	for i, service := range services {
		indexes[service.Name] = i
		if previous, exists := statesByName[service.Name]; exists {
			newState[i] = previous
			shouldRun[i] = shouldRunByName[service.Name]
		} else {
			newState[i].status = "starting"
			shouldRun[i] = true
		}
		newState[i].appendLine("[config reloaded]")
		if shouldRun[i] {
			newState[i].status = "starting"
		}
	}
	m.services, m.state = services, newState
	m.panels = nil
	for _, name := range panelNames {
		if index, exists := indexes[name]; exists {
			present := false
			for _, panelService := range m.panels {
				if panelService == index {
					present = true
					break
				}
			}
			if !present {
				m.panels = append(m.panels, index)
			}
		}
	}
	m.selected = 0
	if index, exists := indexes[oldSelected]; exists {
		m.selected = index
	}
	m.resizePanels()
	m.selectService(m.selected)
	return func() tea.Msg {
		m.super.reconfigure(services, shouldRun)
		return nil
	}
}

func (m *model) adjustPanelCount(delta int) {
	_, _, _, automaticCapacity, _ := m.layout()
	automaticCapacity = max(1, min(len(m.services), automaticCapacity))
	current := len(m.panels)
	if m.panelLimit > 0 {
		current = m.panelLimit
	}
	target := max(1, min(automaticCapacity, current+delta))
	if target == automaticCapacity {
		m.panelLimit = 0
	} else {
		m.panelLimit = target
	}
	m.resizePanels()
}

func (m model) layout() (sidebarWidth, gridWidth, columns, capacity, bodyHeight int) {
	width := max(1, m.width)
	height := max(1, m.height)
	sidebarWidth = min(26, max(14, width/4))
	if sidebarWidth >= width {
		sidebarWidth = max(1, width/3)
	}
	gridWidth = max(1, width-sidebarWidth-1)
	columns = max(1, gridWidth/minPanelWidth)
	bodyHeight = max(4, height-4)
	rows := max(1, bodyHeight/minPanelHeight)
	capacity = columns * rows
	return
}

func (m *model) assignSelectedTo(target int) {
	if len(m.panels) == 0 || len(m.services) == 0 {
		return
	}
	target = max(0, min(len(m.panels)-1, target))
	for panel, service := range m.panels {
		if service == m.selected {
			m.panels[panel], m.panels[target] = m.panels[target], m.panels[panel]
			m.focusedPanel = target
			m.assigningPanel = false
			return
		}
	}
	m.panels[target] = m.selected
	m.focusedPanel = target
	m.assigningPanel = false
}

func panelIndexForKey(key string) (int, bool) {
	key = strings.ToLower(key)
	if len(key) != 1 || key[0] < 'a' || key[0] > 'z' {
		return 0, false
	}
	return int(key[0] - 'a'), true
}

func panelLetter(index int) string {
	if index < 0 {
		return "?"
	}
	var label string
	for index >= 0 {
		label = string(rune('A'+index%26)) + label
		index = index/26 - 1
	}
	return label
}

func (m *model) activeService() int {
	if m.sidebarFocused || len(m.panels) == 0 {
		return m.selected
	}
	m.focusedPanel = max(0, min(len(m.panels)-1, m.focusedPanel))
	return m.panels[m.focusedPanel]
}

func (m *model) activeState() *serviceState {
	return &m.state[m.activeService()]
}

func (m *model) scrollFocused(delta int) {
	state := m.activeState()
	state.scroll = min(max(0, len(state.lines)-1), max(0, state.scroll+delta))
}

func (m model) startCmd(index int) tea.Cmd {
	return func() tea.Msg {
		m.super.start(index)
		return nil
	}
}

func (m model) stopCmd(index int) tea.Cmd {
	return func() tea.Msg {
		m.super.stop(index)
		return nil
	}
}

func (m model) restartCmd(index int) tea.Cmd {
	return func() tea.Msg {
		m.super.restart(index)
		return nil
	}
}

func (m model) View() tea.View {
	if m.width < 1 || m.height < 1 {
		return tea.NewView("Starting…")
	}
	if len(m.services) == 0 {
		return tea.NewView("No services configured.\n")
	}
	if len(m.panels) == 0 {
		m.panels = []int{0}
	}
	sidebarWidth, gridWidth, maxColumns, _, bodyHeight := m.layout()
	columns := min(maxColumns, len(m.panels))
	rows := int(math.Ceil(float64(len(m.panels)) / float64(columns)))
	panelHeight := max(3, bodyHeight/rows)
	panelWidthBase, panelWidthExtra := gridWidth/columns, gridWidth%columns

	var panelRows []string
	for row := 0; row < rows; row++ {
		var rowPanels []string
		for col := 0; col < columns; col++ {
			panelPosition := row*columns + col
			width := panelWidthBase
			if col < panelWidthExtra {
				width++
			}
			if panelPosition >= len(m.panels) {
				continue
			}
			serviceIndex := m.panels[panelPosition]
			rowPanels = append(rowPanels, m.renderPanel(panelPosition, serviceIndex, width, panelHeight))
		}
		panelRows = append(panelRows, lipgloss.JoinHorizontal(lipgloss.Top, rowPanels...))
	}
	grid := lipgloss.JoinVertical(lipgloss.Left, panelRows...)
	if rows*panelHeight < bodyHeight {
		grid += strings.Repeat("\n", bodyHeight-rows*panelHeight)
	}
	sidebar := m.renderSidebar(sidebarWidth, bodyHeight)
	body := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, " ", grid)

	header := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colorText)).Render("SKEW")
	header += "  " + lipgloss.NewStyle().Foreground(lipgloss.Color(colorAccent)).Render("LOCAL DEV")
	panelSetting := "auto"
	if m.panelLimit > 0 {
		panelSetting = fmt.Sprint(m.panelLimit)
	}
	header += "  " + lipgloss.NewStyle().Foreground(lipgloss.Color(colorMuted)).Render(
		fmt.Sprintf("%s  %d col  %s panels · panel %s/%s", m.statusSummary(), columns, panelSetting, panelLetter(m.focusedPanel), panelLetter(len(m.panels)-1)),
	)
	cta := lipgloss.NewStyle().Foreground(lipgloss.Color(colorAccent)).Bold(true)
	help := "←/→ h/l panels  ↑/↓ j/k list or logs  Tab switch  " + cta.Render("Enter assign")
	if m.assigningPanel {
		help = cta.Render("A-Z choose directly · AA+ use arrows + Space") + "  Esc cancel  Ctrl+C quit"
	} else {
		help += "\n+/- panels  0 auto  s start/stop  r restart  c clear  q quit"
	}
	content := header + "\n" + body + "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color(colorMuted)).Render(help)
	view := tea.NewView(content)
	view.AltScreen = true
	view.BackgroundColor = lipgloss.Color(colorBackground)
	view.ForegroundColor = lipgloss.Color(colorText)
	return view
}

func (m model) statusSummary() string {
	running, failed := 0, 0
	for _, state := range m.state {
		switch state.status {
		case "running":
			running++
		case "failed":
			failed++
		}
	}
	return fmt.Sprintf("%d/%d running · %d failed", running, len(m.services), failed)
}

func (m model) renderSidebar(width, height int) string {
	contentWidth := max(1, width-2)
	contentHeight := max(1, height-2)
	lines := []string{"SERVICES"}
	assignedPanel := make(map[int]int, len(m.panels))
	for panel, service := range m.panels {
		assignedPanel[service] = panel
	}
	visibleItems := max(0, contentHeight-1)
	start := min(m.listOffset, max(0, len(m.services)-visibleItems))
	end := min(len(m.services), start+visibleItems)
	for i := start; i < end; i++ {
		name := m.services[i].Name
		mark := "○"
		color := colorMuted
		switch m.state[i].status {
		case "running":
			mark, color = "●", colorAccent
		case "starting", "stopping":
			mark, color = "●", colorMuted
		case "failed":
			mark, color = "●", "#FF6B6B"
		case "exited":
			mark, color = "●", colorMuted
		}
		label := mark + " " + name
		if panel, assigned := assignedPanel[i]; assigned {
			label += fmt.Sprintf("  [%s]", panelLetter(panel))
		}
		item := ansi.Truncate(label, contentWidth, "…")
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
		if i == m.selected {
			style = style.Bold(true).Foreground(lipgloss.Color(colorAccent)).Background(lipgloss.Color("#101811"))
		}
		lines = append(lines, style.Render(item))
	}
	for len(lines) < contentHeight {
		lines = append(lines, "")
	}
	border := lipgloss.Color(colorLine)
	if m.sidebarFocused {
		border = lipgloss.Color(colorAccent)
	}
	return lipgloss.NewStyle().Width(contentWidth).Height(contentHeight).
		Border(lipgloss.NormalBorder()).BorderForeground(border).
		Render(strings.Join(lines, "\n"))
}

func (m model) renderPanel(panelPosition, serviceIndex, width, height int) string {
	service, state := m.services[serviceIndex], m.state[serviceIndex]
	contentWidth := max(1, width-2)
	contentHeight := max(1, height-2)
	statusColor := colorMuted
	switch state.status {
	case "running":
		statusColor = colorAccent
	case "starting", "stopping":
		statusColor = colorMuted
	case "failed":
		statusColor = "#FF6B6B"
	case "exited":
		statusColor = colorMuted
	}
	marker := ""
	if m.assigningPanel && panelPosition == m.focusedPanel {
		marker = "› "
	}
	title := fmt.Sprintf("%s[%s] %s  %s", marker, panelLetter(panelPosition), service.Name, state.status)
	if state.status == "failed" || state.status == "exited" {
		title += fmt.Sprintf(" (%d)", state.exitCode)
	}
	lines := []string{title, service.Command}
	available := max(0, contentHeight-len(lines))
	end := len(state.lines) - state.scroll
	start := max(0, end-available)
	if end > len(state.lines) {
		end = len(state.lines)
	}
	for i := start; i < end; i++ {
		lines = append(lines, state.lines[i])
	}
	if len(lines) > contentHeight {
		lines = lines[:contentHeight]
	}
	for len(lines) < contentHeight {
		lines = append(lines, "")
	}
	border := lipgloss.Color(colorLine)
	if m.assigningPanel {
		border = lipgloss.Color(colorAccent)
	} else if !m.sidebarFocused && panelPosition == m.focusedPanel {
		border = lipgloss.Color(colorAccent)
	}
	styledLines := make([]string, len(lines))
	for i, line := range lines {
		if i == 0 {
			styledLines[i] = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(statusColor)).Render(ansi.Truncate(line, contentWidth, "…"))
		} else {
			styledLines[i] = ansi.Truncate(line, contentWidth, "…")
		}
	}
	return lipgloss.NewStyle().Width(contentWidth).Height(contentHeight).
		Border(lipgloss.NormalBorder()).BorderForeground(border).
		Render(strings.Join(styledLines, "\n"))
}
