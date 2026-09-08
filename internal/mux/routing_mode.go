package mux

import "github.com/TheDaniXSX/codex-subscription-router-windows/internal/state"

func (m *Multiplexer) RoutingMode() state.RoutingMode { return m.store.RoutingMode() }

func (m *Multiplexer) SetRoutingMode(mode state.RoutingMode) error {
	previous := m.RoutingMode()
	if err := m.store.SetRoutingMode(mode); err != nil {
		return err
	}
	m.publishRoutingModeChange(previous)
	return nil
}

func (m *Multiplexer) publishRoutingModeChange(previous state.RoutingMode) {
	mode := m.RoutingMode()
	if previous != mode {
		m.publish(Event{Type: "routing-mode-updated", AccountID: mode.AccountID})
	}
}
