package homeassistant

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/gonzalop/bambulan"
)

func TestDashboardEntitiesMatchPublishedDiscovery(t *testing.T) {
	// Read dashboard template
	data, err := os.ReadFile("dashboard.yaml")
	if err != nil {
		t.Fatalf("Failed to read dashboard.yaml: %v", err)
	}

	content := string(data)

	// Extract all entity references: "entity: <entity_id>"
	re := regexp.MustCompile(`entity:\s+([^\s]+)`)
	matches := re.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		t.Fatal("No entity references found in dashboard.yaml")
	}

	// Create DiscoveryFactory for a test model/serial with full capabilities (e.g. X1E / C13)
	model := "p1s"
	serial := "01S00A12345678"
	last4 := "5678"
	displayName := "Bambu Lab P1S " + last4
	factory := NewDiscoveryFactory("homeassistant", serial, model, displayName)
	b := &Bridge{prefix: "homeassistant", serial: serial}

	publishedEntities := make(map[string]bool)
	for _, entry := range b.discoveryEntries(factory, bambulan.CapabilitiesForModel("C13"), []string{"None"}) {
		publishedEntities[entry.component+"."+entry.cfg.ObjectID] = true
	}

	// Validate all entities in dashboard.yaml match a published entity
	for _, match := range matches {
		rawEntity := match[1]
		// Replace template variables <MODEL> and <LAST_4_SERIAL>
		entityID := strings.ReplaceAll(rawEntity, "<MODEL>", model)
		entityID = strings.ReplaceAll(entityID, "<LAST_4_SERIAL>", last4)

		if !publishedEntities[entityID] {
			t.Errorf("Dashboard entity %q (raw: %q) is not published in Home Assistant discovery!", entityID, rawEntity)
		}
	}
}
