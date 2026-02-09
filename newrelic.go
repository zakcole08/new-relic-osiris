package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Entity struct {
	Name           string
	GUID           string
	Type           string
	HasAlert       bool
	AlertType      string
	AlertMessage   string
	ConnectionInfo string
	OS             string
}

type EntityList struct {
	Entities []*Entity
	Error    string
}

type NerdGraphQuery struct {
	Query string `json:"query"`
}

type NerdGraphResponse struct {
	Data struct {
		Actor struct {
			Entities []struct {
				Name       string `json:"name"`
				GUID       string `json:"guid"`
				EntityType string `json:"entityType"`
				Incidents  []struct {
					Title       string `json:"title"`
					Description string `json:"description"`
					Severity    string `json:"severity"`
				} `json:"incidents"`
			} `json:"entities"`
		} `json:"actor"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func FetchEntities(config *Config) *EntityList {
	list := &EntityList{
		Entities: make([]*Entity, 0),
	}

	if config.APIKey == "" || config.AccountID == "" {
		list.Error = "API key or account ID not configured"
		return addTestEntities(list)
	}

	// NerdGraph query to fetch Host entities (without violations - fetch separately)
	// Filtered to infrastructure hosts/servers
	query := `{
		actor {
			entitySearch(query: "domain = 'INFRA' AND type = 'HOST'") {
				results {
					entities {
						guid
						name
						entityType
					}
				}
			}
		}
	}`

	payload := NerdGraphQuery{Query: query}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		list.Error = "Error marshaling request: " + err.Error()
		return addTestEntities(list)
	}

	req, err := http.NewRequest("POST", "https://api.newrelic.com/graphql", bytes.NewReader(payloadBytes))
	if err != nil {
		list.Error = "Error creating request: " + err.Error()
		debugLog("Error creating request: " + err.Error())
		return addTestEntities(list)
	}

	debugLog(fmt.Sprintf("Fetching entities with API key: %s (first 10 chars)", config.APIKey[:10]))

	req.Header.Set("API-Key", config.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		list.Error = "Error fetching from New Relic: " + err.Error()
		debugLog("Fetch failed: " + err.Error())
		return addTestEntities(list)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		list.Error = "Error reading response: " + err.Error()
		debugLog("Read failed: " + err.Error())
		return addTestEntities(list)
	}

	debugLog(fmt.Sprintf("API Response Status: %d", resp.StatusCode))
	debugLog(fmt.Sprintf("API Response Body: %s", string(body)))

	// Try to parse response and check for errors
	var nrResp map[string]interface{}
	if err := json.Unmarshal(body, &nrResp); err != nil {
		list.Error = "Error parsing response: " + err.Error()
		debugLog("JSON parse failed: " + err.Error())
		return addTestEntities(list)
	}

	// Check for GraphQL errors
	if errors, ok := nrResp["errors"].([]interface{}); ok && len(errors) > 0 {
		errorMsg := fmt.Sprintf("%v", errors[0])
		list.Error = "New Relic API error: " + errorMsg
		debugLog("GraphQL error: " + errorMsg)
		return addTestEntities(list)
	}

	debugLog("Query successful, parsing entities...")

	// Parse entities from response
	if data, ok := nrResp["data"].(map[string]interface{}); ok {
		if actor, ok := data["actor"].(map[string]interface{}); ok {
			if search, ok := actor["entitySearch"].(map[string]interface{}); ok {
				if results, ok := search["results"].(map[string]interface{}); ok {
					if entities, ok := results["entities"].([]interface{}); ok {
						debugLog(fmt.Sprintf("Found %d entities", len(entities)))
						for _, entityData := range entities {
							if entityMap, ok := entityData.(map[string]interface{}); ok {
								entity := &Entity{}
								
								if name, ok := entityMap["name"].(string); ok {
									entity.Name = name
								}
								if guid, ok := entityMap["guid"].(string); ok {
									entity.GUID = guid
								}
								if etype, ok := entityMap["entityType"].(string); ok {
									entity.Type = etype
								}
								
								if entity.Name != "" {
									debugLog(fmt.Sprintf("Parsed entity: %s (type: %s)", entity.Name, entity.Type))
									list.Entities = append(list.Entities, entity)
								}
							}
						}
					}
				}
			}
		}
	}

	return list
}

func fetchIncidents(config *Config, list *EntityList) {
	defer func() {
		if r := recover(); r != nil {
			debugLog(fmt.Sprintf("fetchIncidents panic: %v", r))
		}
	}()

	debugLog("fetchIncidents: starting probe")
	// We'll send a generic query that may return incidents under several fields.
	// If the schema differs, we parse the response generically to find incident objects
	// and extract any GUIDs mentioned.
	query := `{
		actor {
			# Try several possible incident-related root fields; server will ignore unknown ones
			incidentCommandCenter { __typename }
			incidentManagement { __typename }
			incident { __typename }
			incidentCommandCenterSummary: incidentCommandCenter { __typename }
		}
	}`

	payload := NerdGraphQuery{Query: query}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		debugLog("Error marshaling incidents request: " + err.Error())
		return
	}

	req, err := http.NewRequest("POST", "https://api.newrelic.com/graphql", bytes.NewReader(payloadBytes))
	if err != nil {
		debugLog("Error creating incidents request: " + err.Error())
		return
	}

	req.Header.Set("API-Key", config.APIKey)
	req.Header.Set("Content-Type", "application/json")

	// Use context with timeout for the request
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		debugLog("Error fetching incidents: " + err.Error())
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		debugLog("Error reading incidents response: " + err.Error())
		return
	}

	debugLog(fmt.Sprintf("Incidents API response (probe): %s", string(body)))

	// Attempt to parse any incidents/guid mentions in the returned JSON using a generic extractor
	incidents := extractIncidentsGeneric(body)
	if len(incidents) == 0 {
		debugLog("No incidents parsed from generic probe response; attempting REST fallback")
		// Try REST alerts/violations API fallback (non-blocking with timeout)
		done := make(chan struct{})
		go func() {
			defer close(done)
			fetchViolationsREST(config, list)
		}()
		select {
		case <-done:
			debugLog("fetchIncidents: REST fallback completed")
		case <-time.After(15 * time.Second):
			debugLog("fetchIncidents: REST fallback timed out")
		}
		return
	}

	// Match parsed incidents to entities
	matched := 0
	for _, inc := range incidents {
		for _, guid := range inc.GUIDs {
			for _, entity := range list.Entities {
				if entity.GUID == guid {
					entity.HasAlert = true
					if inc.Title != "" {
						entity.AlertType = inc.Title
					}
					entity.AlertMessage = inc.Description
					debugLog(fmt.Sprintf("Matched generic incident to %s: %s", entity.Name, inc.Title))
					matched++
				}
			}
		}
	}
	debugLog(fmt.Sprintf("Matched %d incidents to entities (generic)", matched))
	debugLog("fetchIncidents: completed")
}

// incidentGeneric holds parsed incident info from arbitrary GraphQL responses
type incidentGeneric struct {
	Title       string
	Description string
	GUIDs       []string
}

// extractIncidentsGeneric walks a JSON graph and pulls out objects that look like incidents
// It returns a slice of incidentGeneric with associated GUIDs found nearby.
func extractIncidentsGeneric(body []byte) []incidentGeneric {
	var root interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		debugLog("extractIncidentsGeneric: json unmarshal error: " + err.Error())
		return nil
	}

	var found []incidentGeneric

	var walk func(node interface{})
	walk = func(node interface{}) {
		switch v := node.(type) {
		case map[string]interface{}:
			// If this object looks like an incident (has "title" or "description"), try to extract GUIDs nearby
			title := ""
			desc := ""
			if t, ok := v["title"].(string); ok {
				title = t
			}
			if d, ok := v["description"].(string); ok {
				desc = d
			}

			guids := make([]string, 0)
			// Direct keys containing entity GUIDs
			if g, ok := v["entityGuid"].(string); ok {
				guids = append(guids, g)
			}
			if g, ok := v["guid"].(string); ok {
				guids = append(guids, g)
			}

			// Look for arrays under several likely keys
			for _, key := range []string{"affectedEntities", "impactedEntities", "entities", "impacted_entity_list", "affected_entity_list"} {
				if arr, ok := v[key].([]interface{}); ok {
					for _, item := range arr {
						if imap, ok := item.(map[string]interface{}); ok {
							if g, ok := imap["entityGuid"].(string); ok {
								guids = append(guids, g)
							}
							if g, ok := imap["guid"].(string); ok {
								guids = append(guids, g)
							}
						}
					}
				}
			}

			if title != "" || desc != "" {
				if len(guids) > 0 {
					found = append(found, incidentGeneric{Title: title, Description: desc, GUIDs: guids})
				}
			}

			// Continue walking children
			for _, child := range v {
				walk(child)
			}
		case []interface{}:
			for _, item := range v {
				walk(item)
			}
		}
	}

	walk(root)
	return found
}

// fetchViolationsREST calls New Relic classic Alerts Violations REST API as a fallback
func fetchViolationsREST(config *Config, list *EntityList) {
	defer func() {
		if r := recover(); r != nil {
			debugLog(fmt.Sprintf("fetchViolationsREST panic: %v", r))
		}
	}()

	url := "https://api.newrelic.com/v2/alerts_violations.json?only_open=true"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		debugLog("fetchViolationsREST: request create error: " + err.Error())
		return
	}
	// v2 REST API expects X-Api-Key header
	req.Header.Set("X-Api-Key", config.APIKey)
	req.Header.Set("Content-Type", "application/json")

	// Use context timeout to ensure this cannot hang indefinitely
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		debugLog("fetchViolationsREST: http error: " + err.Error())
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		debugLog("fetchViolationsREST: read error: " + err.Error())
		return
	}

	debugLog(fmt.Sprintf("Violations REST response: %s", string(body)))

	var respObj map[string]interface{}
	if err := json.Unmarshal(body, &respObj); err != nil {
		debugLog("fetchViolationsREST: json unmarshal error: " + err.Error())
		return
	}

	violations, _ := respObj["violations"].([]interface{})
	matched := 0
	for _, v := range violations {
		if vmap, ok := v.(map[string]interface{}); ok {
			title := ""
			details := ""
			targetNames := make([]string, 0)

			if t, ok := vmap["condition_name"].(string); ok {
				title = t
			}
			if d, ok := vmap["details"].(string); ok {
				details = d
			}

			// Try to extract target name(s)
			if targets, ok := vmap["targets"].([]interface{}); ok {
				for _, ti := range targets {
					if tmap, ok := ti.(map[string]interface{}); ok {
						if name, ok := tmap["name"].(string); ok {
							targetNames = append(targetNames, name)
						}
					}
				}
			}

			// Also check links.entity or entity_name
			if links, ok := vmap["links"].(map[string]interface{}); ok {
				if en, ok := links["entity"].(string); ok {
					targetNames = append(targetNames, en)
				}
			}
			if ename, ok := vmap["entity_name"].(string); ok {
				targetNames = append(targetNames, ename)
			}
			// Also check nested entity object
			if entObj, ok := vmap["entity"].(map[string]interface{}); ok {
				if en, ok := entObj["name"].(string); ok {
					targetNames = append(targetNames, en)
				}
			}

			// Try to match targets to entities by name (case-insensitive substring)
			for _, tn := range targetNames {
				for _, entity := range list.Entities {
					if strings.Contains(strings.ToLower(entity.Name), strings.ToLower(tn)) || strings.Contains(strings.ToLower(tn), strings.ToLower(entity.Name)) {
						entity.HasAlert = true
						if title != "" {
							entity.AlertType = title
						}
						entity.AlertMessage = details
						debugLog(fmt.Sprintf("Matched REST violation to %s via name '%s'", entity.Name, tn))
						matched++
					}
				}
			}
		}
	}
	debugLog(fmt.Sprintf("Matched %d REST violations to entities", matched))
}

func addTestEntities(list *EntityList) *EntityList {
	// Test entities for development/demo
	list.Entities = []*Entity{
		{
			Name:         "web-01",
			Type:         "HOST",
			HasAlert:     false,
			OS:           "Linux",
			ConnectionInfo: "192.168.1.10",
		},
		{
			Name:           "api-02",
			Type:           "HOST",
			HasAlert:       true,
			AlertType:      "CPU High",
			AlertMessage:   "CPU > 85%",
			OS:             "Linux",
			ConnectionInfo: "192.168.1.11",
		},
		{
			Name:           "db-01",
			Type:           "HOST",
			HasAlert:       false,
			OS:             "Linux",
			ConnectionInfo: "192.168.1.12",
		},
		{
			Name:           "cache-01",
			Type:           "HOST",
			HasAlert:       true,
			AlertType:      "Memory",
			AlertMessage:   "Memory > 90%",
			OS:             "Linux",
			ConnectionInfo: "192.168.1.13",
		},
		{
			Name:           "monitor-01",
			Type:           "HOST",
			HasAlert:       false,
			OS:             "Linux",
			ConnectionInfo: "192.168.1.14",
		},
	}
	return list
}
