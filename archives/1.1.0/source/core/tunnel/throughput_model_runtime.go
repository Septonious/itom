package tunnel

import "itom.local/core/wire"

const modelRetainsWindow = true

// Runtime factories select the experiment for both real endpoints and every
// rotated logical generation. The original factories remain the RC2 controller
// for its unchanged contract/unit tests and the independent reference build.
func newModelStripeGroup(logical wire.ID, generation uint64) *stripeGroup {
	g := newStripeGroup(logical, generation)
	g.delivery.modelEnabled = true
	g.delivery.model.retainWindow = modelRetainsWindow
	return g
}
func newModelStripeHub() *stripeHub {
	h := newStripeHub()
	h.modelEnabled = true
	return h
}
func (h *stripeHub) configureModel(g *stripeGroup) {
	if h.modelEnabled {
		g.delivery.modelEnabled = true
		g.delivery.model.retainWindow = modelRetainsWindow
	}
}
