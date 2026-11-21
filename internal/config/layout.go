// Package config loads the material-flow layout file (MFC_LAYOUT_FILE, ADR 0002).
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/IQVO/material-flow-control/internal/domain/layout"
)

const (
	defaultRetrievalsPerUnitHour = 60
	defaultLegsPerRobotHour      = 30
)

type fileLayout struct {
	SiteID string `yaml:"siteId"`
	Fleet  struct {
		FleetID          string `yaml:"fleetId"`
		LegsPerRobotHour int    `yaml:"legsPerRobotHour"`
	} `yaml:"fleet"`
	Asrs []struct {
		AsrsID                string `yaml:"asrsId"`
		LocationCode          string `yaml:"locationCode"`
		RetrievalsPerUnitHour int    `yaml:"retrievalsPerUnitHour"`
		Ports                 []struct {
			PortID         string `yaml:"portId"`
			Kind           string `yaml:"kind"`
			LocationCode   string `yaml:"locationCode"`
			HandoffPointID string `yaml:"handoffPointId"`
		} `yaml:"ports"`
	} `yaml:"asrs"`
	Stations []struct {
		StationID      string `yaml:"stationId"`
		LocationCode   string `yaml:"locationCode"`
		HandoffPointID string `yaml:"handoffPointId"`
		BufferLimit    int    `yaml:"bufferLimit"`
	} `yaml:"stations"`
	Handoffs []struct {
		LocationCode   string `yaml:"locationCode"`
		HandoffPointID string `yaml:"handoffPointId"`
		Kind           string `yaml:"kind"`
	} `yaml:"handoffs"`
	ClarificationPoints []struct {
		LocationCode   string `yaml:"locationCode"`
		HandoffPointID string `yaml:"handoffPointId"`
	} `yaml:"clarificationPoints"`
}

// LoadLayout reads and validates the layout file at path.
func LoadLayout(path string) (*layout.Layout, error) {
	b, err := os.ReadFile(filepath.Clean(path)) //nolint:gosec // the layout path is operator-supplied configuration
	if err != nil {
		return nil, fmt.Errorf("read layout %s: %w", path, err)
	}
	return ParseLayout(b)
}

// ParseLayout decodes (unknown keys are an error) and validates a layout document.
func ParseLayout(b []byte) (*layout.Layout, error) {
	var f fileLayout
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("decode layout: %w", err)
	}
	return layout.New(f.spec())
}

func orDefault(v, d int) int {
	if v == 0 {
		return d
	}
	return v
}

func (f fileLayout) spec() layout.Spec {
	s := layout.Spec{SiteID: f.SiteID, Fleet: layout.Fleet{FleetID: f.Fleet.FleetID,
		LegsPerRobotHour: orDefault(f.Fleet.LegsPerRobotHour, defaultLegsPerRobotHour)}}
	for _, a := range f.Asrs {
		as := layout.Asrs{AsrsID: a.AsrsID, LocationCode: a.LocationCode,
			RetrievalsPerUnitHour: orDefault(a.RetrievalsPerUnitHour, defaultRetrievalsPerUnitHour)}
		for _, p := range a.Ports {
			as.Ports = append(as.Ports, layout.Port{PortID: p.PortID, Kind: layout.PortKind(p.Kind),
				LocationCode: p.LocationCode, HandoffPointID: p.HandoffPointID})
		}
		s.Asrs = append(s.Asrs, as)
	}
	for _, st := range f.Stations {
		s.Stations = append(s.Stations, layout.Station{StationID: st.StationID, LocationCode: st.LocationCode,
			HandoffPointID: st.HandoffPointID, BufferLimit: st.BufferLimit})
	}
	for _, h := range f.Handoffs {
		s.Handoffs = append(s.Handoffs, layout.Handoff{LocationCode: h.LocationCode, HandoffPointID: h.HandoffPointID,
			Kind: layout.HandoffKind(h.Kind)})
	}
	for _, c := range f.ClarificationPoints {
		s.ClarificationPoints = append(s.ClarificationPoints, layout.ClarificationPoint{LocationCode: c.LocationCode,
			HandoffPointID: c.HandoffPointID})
	}
	return s
}
