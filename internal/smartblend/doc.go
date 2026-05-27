// Package smartblend builds the per-controller blend hook that bridges
// the smart-mode learning runtimes (Layer-A/B/C confidence, the R12
// aggregator, the IMC-PI BlendedController) into the reactive control
// loop. BuildFn returns a controller.BlendFn closure that the daemon
// installs via controller.WithBlend.
//
// This wiring previously lived inline in cmd/ventd (package main), where
// it could be neither imported nor unit-tested (R6c of the architecture
// review). The composition root still owns the SmartModeBundle and hands
// the blend-relevant runtimes here via Deps; the clamp-before-blend
// safety contract remains in internal/controller.
package smartblend
