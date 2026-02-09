package main

import (
    "bufio"
    "fmt"
    "os"
    "os/exec"
    "runtime"
    "strings"
    "sync"
    "time"

    "github.com/gdamore/tcell/v2"
    "github.com/rivo/tview"
)

type AppState struct {
    entities          []*Entity
    lastRefresh       time.Time
    refreshInProgress bool
    mu                sync.Mutex
    selectedIndex     int
    errMsg            string
    searchQuery       string
    lastSearchPos     int
}

func main() {
    // Check for --debug flag
    for _, arg := range os.Args[1:] {
        if arg == "--debug" {
            DebugEnabled = true
            break
        }
    }

    config := LoadConfig()
    debugLog("=== OSIRIS STARTED ===")
    debugLog("API Key set: " + fmt.Sprintf("%v", config.APIKey != ""))
    debugLog("Account ID set: " + fmt.Sprintf("%v", config.AccountID != ""))

    state := &AppState{lastRefresh: time.Now()}

    app := tview.NewApplication()

    // Main list view
    list := tview.NewList().ShowSecondaryText(false).SetWrapAround(true)

    // Status bar
    statusText := tview.NewTextView().SetDynamicColors(true)
    statusText.SetText("[yellow]⟳ Loading entities from New Relic...")
    statusText.SetBorder(false)

    // Details view for selected item
    detailsText := tview.NewTextView().SetDynamicColors(true)
    detailsText.SetBorder(true).SetTitle(" Alert Details ")

    // Flex layout
    flex := tview.NewFlex().SetDirection(tview.FlexRow).
        AddItem(statusText, 1, 0, false).
        AddItem(list, 0, 1, true).
        AddItem(detailsText, 5, 0, false)

    // Start heartbeat for debugging
    go startHeartbeat()

    // Initial fetch
    go refreshEntities(state, config, list, statusText, detailsText, app)

    // Auto-refresh ticker
    ticker := time.NewTicker(time.Duration(config.RefreshInterval) * time.Second)
    defer ticker.Stop()
    go func() {
        for range ticker.C {
            refreshEntities(state, config, list, statusText, detailsText, app)
        }
    }()

    // List selection handler (activated/Enter)
    list.SetSelectedFunc(func(index int, mainText string, secondaryText string, shortcut rune) {
        showDetails(index, state, detailsText)
    })

    // Track highlight changes (arrow keys)
    list.SetChangedFunc(func(index int, mainText string, secondaryText string, shortcut rune) {
        showDetails(index, state, detailsText)
    })

    // Input handler
    list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
        switch event.Key() {
        case tcell.KeyCtrlC, tcell.KeyEsc:
            if event.Rune() == 'q' || event.Rune() == 'Q' {
                app.Stop()
                return nil
            }
        case tcell.KeyRune:
            switch event.Rune() {
            case 'q', 'Q':
                app.Stop()
                return nil
            case ' ':
                go refreshEntities(state, config, list, statusText, detailsText, app)
                return nil
            case '/':
                app.Suspend(func() {
                    fmt.Print("Search for server: ")
                    reader := bufio.NewReader(os.Stdin)
                    q, _ := reader.ReadString('\n')
                    q = strings.TrimSpace(q)
                    state.mu.Lock()
                    state.searchQuery = q
                    state.lastSearchPos = -1
                    state.mu.Unlock()
                })
                if state.searchQuery != "" {
                    found := findNextMatch(state)
                    if found >= 0 {
                        list.SetCurrentItem(found)
                        showDetails(found, state, detailsText)
                    }
                }
                return nil
            case 'n', 'N':
                if state.searchQuery != "" {
                    found := findNextMatch(state)
                    if found >= 0 {
                        list.SetCurrentItem(found)
                        showDetails(found, state, detailsText)
                    }
                }
                return nil
            case 's', 'S':
                // SSH
                state.mu.Lock()
                if state.selectedIndex >= 0 && state.selectedIndex < len(state.entities) {
                    entity := state.entities[state.selectedIndex]
                    state.mu.Unlock()

                    debugLog(fmt.Sprintf("Launching SSH to %s", entity.Name))
                    debugLog("about to suspend (SSH)")
                    app.Suspend(func() {
                        defer func() {
                            if r := recover(); r != nil {
                                debugLog(fmt.Sprintf("panic in SSH suspend: %v", r))
                            }
                        }()
                        debugLog("in suspend (SSH): preparing to exec")
                        fmt.Fprintf(os.Stderr, "\n[osiris] Launching SSH to %s\n", entity.Name)
                        fmt.Fprintf(os.Stderr, "[osiris] Type 'exit' or Ctrl+D to return to osiris\n\n")
                        execCmd := exec.Command("ssh", "admin@"+entity.Name)
                        execCmd.Stdin = os.Stdin
                        execCmd.Stdout = os.Stdout
                        execCmd.Stderr = os.Stderr
                        if err := execCmd.Run(); err != nil {
                            debugLog("SSH error: " + err.Error())
                        }
                        debugLog("in suspend (SSH): exec.Run returned")
                    })

                    debugLog("returned from suspend (SSH)")
                    // small pause to allow terminal to be restored
                    time.Sleep(100 * time.Millisecond)
                    go func() {
                        debugLog("attempting suspend-resume to force terminal reset (SSH)")
                        app.Suspend(func() {})
                        time.Sleep(50 * time.Millisecond)
                        app.QueueUpdateDraw(func() {
                            debugLog("queueing redraw after SSH suspend (via suspend-resume)")
                            updateListView(list, state, statusText, detailsText, app)
                        })
                    }()
                    return nil
                }
                state.mu.Unlock()
                return nil
            case 'r', 'R':
                // RDP
                state.mu.Lock()
                if state.selectedIndex >= 0 && state.selectedIndex < len(state.entities) {
                    entity := state.entities[state.selectedIndex]
                    state.mu.Unlock()

                    debugLog(fmt.Sprintf("Launching RDP to %s", entity.Name))
                    debugLog("about to suspend (RDP)")
                    app.Suspend(func() {
                        defer func() {
                            if r := recover(); r != nil {
                                debugLog(fmt.Sprintf("panic in RDP suspend: %v", r))
                            }
                        }()
                        debugLog("in suspend (RDP): preparing to exec")
                        fmt.Fprintf(os.Stderr, "\n[osiris] Launching RDP to %s\n", entity.Name)

                        var execCmd *exec.Cmd
                        if runtime.GOOS == "windows" {
                            execCmd = exec.Command("mstsc", "/v:"+entity.Name)
                        } else {
                            if _, err := os.Stat("/mnt/c/Windows/System32/mstsc.exe"); err == nil {
                                execCmd = exec.Command("/mnt/c/Windows/System32/mstsc.exe", "/v:"+entity.Name)
                            } else {
                                execCmd = exec.Command("xfreerdp", "/v:"+entity.Name, "/u:admin", "+clipboard")
                            }
                        }

                        execCmd.Stdin = os.Stdin
                        execCmd.Stdout = os.Stdout
                        execCmd.Stderr = os.Stderr
                        if err := execCmd.Run(); err != nil {
                            debugLog("RDP error: " + err.Error())
                            fmt.Fprintf(os.Stderr, "[osiris] RDP failed: %v\n", err)
                        }
                        debugLog("in suspend (RDP): exec.Run returned")
                    })

                    debugLog("returned from suspend (RDP)")
                    // small pause to allow terminal to be restored
                    time.Sleep(100 * time.Millisecond)
                    // Try a suspend-resume cycle in a background goroutine to force tview/tcell to reinitialise
                    go func() {
                        debugLog("attempting suspend-resume to force terminal reset (RDP)")
                        app.Suspend(func() {})
                        // brief pause after suspend-resume
                        time.Sleep(50 * time.Millisecond)
                        app.QueueUpdateDraw(func() {
                            debugLog("queueing redraw after RDP suspend (via suspend-resume)")
                            updateListView(list, state, statusText, detailsText, app)
                        })
                    }()
                    return nil
                }
                state.mu.Unlock()
                return nil
            }
        }
        return event
    })

    // Title
    titleText := tview.NewTextView().SetDynamicColors(true).
        SetText("[bold]New Relic Incident Console[white] | [dim]↑↓[white] navigate | [dim]s[white] ssh | [dim]r[white] rdp | [dim]space[white] refresh | [dim]q[white] quit")

    titleBox := tview.NewFlex().SetDirection(tview.FlexColumn).AddItem(titleText, 0, 1, false)
    titleBox.SetBorderAttributes(tcell.AttrBold)

    mainFlex := tview.NewFlex().SetDirection(tview.FlexRow).
        AddItem(titleBox, 2, 0, false).
        AddItem(flex, 0, 1, true)

    if err := app.SetRoot(mainFlex, true).Run(); err != nil {
        panic(err)
    }
}

// showDetails updates the detailsText for a given index and stores selectedIndex
func showDetails(index int, state *AppState, detailsText *tview.TextView) {
    detailsText.Clear()
    state.mu.Lock()
    if index < len(state.entities) {
        state.selectedIndex = index
        entity := state.entities[index]
        state.mu.Unlock()

        if entity.HasAlert {
            fmt.Fprintf(detailsText, "[red]🔴 ALERT[white]\n")
            fmt.Fprintf(detailsText, "[red]%s[white]\n", entity.AlertType)
            fmt.Fprintf(detailsText, "%s\n\n", entity.AlertMessage)
            fmt.Fprintf(detailsText, "[yellow]Press 's' for SSH or 'r' for RDP")
        } else {
            fmt.Fprintf(detailsText, "[green]✓ Status: OK[white]\n")
            fmt.Fprintf(detailsText, "No active alerts")
        }
    } else {
        state.mu.Unlock()
    }
}

// findNextMatch searches for the next entity matching state's searchQuery
func findNextMatch(state *AppState) int {
    state.mu.Lock()
    defer state.mu.Unlock()
    q := strings.ToLower(state.searchQuery)
    if q == "" {
        return -1
    }
    start := state.lastSearchPos + 1
    if start < 0 {
        start = 0
    }
    n := len(state.entities)
    for i := 0; i < n; i++ {
        idx := (start + i) % n
        if strings.Contains(strings.ToLower(state.entities[idx].Name), q) {
            state.lastSearchPos = idx
            return idx
        }
    }
    return -1
}

// startHeartbeat writes a periodic heartbeat to the debug log to help detect hangs
func startHeartbeat() {
    for {
        debugLog("heartbeat")
        time.Sleep(5 * time.Second)
    }
}

func refreshEntities(state *AppState, config *Config, list *tview.List, statusText *tview.TextView, detailsText *tview.TextView, app *tview.Application) {
    state.mu.Lock()
    if state.refreshInProgress {
        state.mu.Unlock()
        return
    }
    state.refreshInProgress = true
    state.mu.Unlock()

    // Fetch fresh data
    result := FetchEntities(config)
    newEntities := result.Entities

    state.mu.Lock()
    state.entities = newEntities
    state.errMsg = result.Error
    state.lastRefresh = time.Now()
    state.refreshInProgress = false
    state.mu.Unlock()

    // Update UI (must be done on main thread)
    debugLog("refreshEntities: queuing UI update")
    app.QueueUpdateDraw(func() {
        updateListView(list, state, statusText, detailsText, app)
    })

    // Fetch incidents asynchronously
    if len(newEntities) > 0 {
        debugLog(fmt.Sprintf("refreshEntities: launching async fetchIncidents for %d entities", len(newEntities)))
        go func() {
            fetchIncidents(config, &EntityList{Entities: newEntities})
            debugLog("refreshEntities: async fetchIncidents completed, queuing UI update")
            app.QueueUpdateDraw(func() {
                updateListView(list, state, statusText, detailsText, app)
            })
        }()
    }
}

func updateListView(list *tview.List, state *AppState, statusText *tview.TextView, detailsText *tview.TextView, app *tview.Application) {
    // Copy state under lock to avoid deadlocks when UI callbacks run
    state.mu.Lock()
    entitiesCopy := make([]*Entity, len(state.entities))
    copy(entitiesCopy, state.entities)
    refreshInProgress := state.refreshInProgress
    errMsg := state.errMsg
    lastRefresh := state.lastRefresh
    selected := state.selectedIndex
    state.mu.Unlock()

    // Clear list and set status on UI thread
    list.Clear()

    // Update status
    if refreshInProgress {
        statusText.SetText("[yellow]⟳ Fetching from New Relic...")
    } else if errMsg != "" {
        statusText.SetText(fmt.Sprintf("[red]✗ Error: %s", errMsg))
    } else if len(entitiesCopy) == 0 {
        statusText.SetText("[dim]No entities found. Check API key and account ID.")
        return
    } else {
        secondsAgo := int(time.Since(lastRefresh).Seconds())
        statusText.SetText(fmt.Sprintf("[green]✓[white] Last updated: %d seconds ago", secondsAgo))
    }

    debugLog(fmt.Sprintf("updateListView: populating %d entities (chunked)", len(entitiesCopy)))

    // Populate the list in background batches to avoid hogging the UI thread
    batchSize := 25
    total := len(entitiesCopy)
    if total == 0 {
        return
    }

    // Ensure colors are set early
    list.SetMainTextColor(tcell.ColorWhite)
    list.SetSecondaryTextColor(tcell.ColorGray)
    list.SetShortcutColor(tcell.ColorYellow)

    go func() {
        for start := 0; start < total; start += batchSize {
            end := start + batchSize
            if end > total {
                end = total
            }
            batch := entitiesCopy[start:end]
            s := start

            app.QueueUpdateDraw(func() {
                for j, entity := range batch {
                    i := s + j
                    status := "[green]OK"
                    if entity.HasAlert {
                        status = "[red]ALERT"
                    }
                    text := fmt.Sprintf("%-15s %s", entity.Name, status)
                    idx := i
                    list.AddItem(text, "", 0, func() {
                        list.SetCurrentItem(idx)
                        showDetails(idx, state, detailsText)
                    })
                }

                if end == total {
                    sel := selected
                    if sel < 0 && total > 0 {
                        sel = 0
                    }
                    if sel >= total {
                        sel = 0
                    }
                    if total > 0 {
                        list.SetCurrentItem(sel)
                    }
                }
            })

            time.Sleep(25 * time.Millisecond)
        }
    }()
}
