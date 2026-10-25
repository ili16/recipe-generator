package service

import "testing"

// Every tool a route names must exist, or toolDefinitions would send the model a
// nameless definition and the route would silently lose a tool.
func TestRouteToolsExist(t *testing.T) {
	for in, names := range routeTools {
		for _, n := range names {
			if _, ok := chatTools[n]; !ok {
				t.Errorf("route %q names unknown tool %q", in, n)
			}
		}
	}
}
