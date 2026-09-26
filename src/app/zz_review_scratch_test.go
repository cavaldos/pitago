package app

import (
	"fmt"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"pitago/src/pirpc"
)

func TestZZScratchMeasure(t *testing.T) {
	m := teamPanelModel(t, 100, 24)
	fmt.Println("header", lipgloss.Height(m.renderHeader()), "input", lipgloss.Height(m.renderInput()), "baseVpH", m.baseVpH, "vpW", m.vp.Width, "taW", m.ta.Width())
	fmt.Println("teamPanelH", m.teamPanelH())
	m.cmdOpen = true
	m.cmdItems = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	for i := range m.cmdItems {
		m.Cmds = append(m.Cmds, pirpc.RepoCommand{Name: fmt.Sprintf("cmd%d", i)})
	}
	m.setTeamWidget(teamWidgetFixture(8), "aboveEditor")
	fmt.Println("popupH", m.popupH(), "cmdWin", m.cmdWin(), "vpH after sync", m.vp.Height)
	m.applyPopupH()
	fmt.Println("vpH after applyPopupH", m.vp.Height, "frame", lipgloss.Height(stripANSI(m.View())))
	fmt.Println("--- panel+popup at 40x120 ---")
	m2 := teamPanelModel(t, 120, 40)
	m2.setTeamWidget(teamWidgetFixture(8), "aboveEditor")
	fmt.Println("vpH", m2.vp.Height, "teamPanelH", m2.teamPanelH(), "frame", lipgloss.Height(m2.View()), "header", lipgloss.Height(m2.renderHeader()), "input", lipgloss.Height(m2.renderInput()))
	// what happens with a task widget present?
}
