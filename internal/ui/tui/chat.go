package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/aeroknight786/trialm/internal/llm"
	"github.com/aeroknight786/trialm/internal/upstream"
)

type chatResponseMsg struct {
	text string
	err  error
}
type chatModel struct {
	ctx        context.Context
	client     upstream.Client
	input      textinput.Model
	log        viewport.Model
	messages   []llm.Message
	transcript []string
	busy       bool
}

func RunChat(ctx context.Context, client upstream.Client) error {
	m := New(Config{Context: ctx, Start: Chat})
	m.chat = newChat(ctx, client)
	m.chat.input.Prompt = "You: "
	m.chat.input.CharLimit = 16000
	m.chat.input.Focus()
	_, err := tea.NewProgram(m, programOptions()...).Run()
	if err != nil {
		return err
	}
	return m.Error
}
func (c *chatModel) resize(w, h int) {
	c.log.SetWidth(max(10, w-2))
	c.log.SetHeight(max(3, h-6))
	c.input.SetWidth(max(10, w-8))
}
func (c *chatModel) update(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "enter" && !c.busy {
		text := strings.TrimSpace(c.input.Value())
		if text == "" {
			return nil
		}
		c.input.SetValue("")
		c.busy = true
		c.messages = append(c.messages, llm.Message{Role: "user", Content: text})
		c.transcript = append(c.transcript, "You: "+text)
		c.log.SetContent(strings.Join(c.transcript, "\n\n"))
		c.log.GotoBottom()
		messages := append([]llm.Message(nil), c.messages...)
		return func() tea.Msg {
			var b strings.Builder
			events, err := c.client.Stream(c.ctx, &llm.Request{Messages: messages, MaxTokens: 2048})
			if err == nil {
				for ev := range events {
					if ev.Type == llm.EventText {
						b.WriteString(ev.Text)
					}
					if ev.Type == llm.EventError {
						err = fmt.Errorf("%s", ev.Text)
					}
				}
			}
			return chatResponseMsg{b.String(), err}
		}
	}
	var cmd tea.Cmd
	if msg.String() == "pgup" || msg.String() == "pgdown" {
		c.log, cmd = c.log.Update(msg)
	} else if !c.busy {
		c.input, cmd = c.input.Update(msg)
	}
	return cmd
}
func (c *chatModel) receive(v chatResponseMsg) tea.Cmd {
	c.busy = false
	if v.err != nil {
		c.transcript = append(c.transcript, "Error: "+v.err.Error())
	} else {
		c.messages = append(c.messages, llm.Message{Role: "assistant", Content: v.text})
		c.transcript = append(c.transcript, "Model: "+v.text)
	}
	c.log.SetContent(strings.Join(c.transcript, "\n\n"))
	c.log.GotoBottom()
	return nil
}
func (c *chatModel) view() string {
	state := ""
	if c.busy {
		state = "\nWaiting for the paced reply…"
	}
	return fmt.Sprintf("triaLM chat · cloud answers at local pace\n%s\n%s%s", c.log.View(), c.input.View(), state)
}

func newChat(ctx context.Context, client upstream.Client) chatModel {
	c := chatModel{ctx: ctx, client: client, input: textinput.New(), log: viewport.New(viewport.WithWidth(76), viewport.WithHeight(14))}
	c.log.SoftWrap = true
	c.input.Prompt = "You: "
	c.input.CharLimit = 16000
	c.input.Focus()
	return c
}

// A chat trial keeps this root and starts only a plain server child. Closing
// its private stdin pipe requests a graceful summary on every OS.
type chatStartedMsg struct {
	client upstream.Client
	stop   func() error
	err    error
}

func (m *Root) startChat(args []string) tea.Cmd {
	m.busy = true
	return func() tea.Msg {
		runArgs := append([]string{"try", "--serve", "--json", "--port", "0"}, args...)
		cmd := exec.CommandContext(m.cfg.Context, m.cfg.Executable, runArgs...)
		cmd.Env = append(childEnv(os.Environ()), "TRIALM_PARENT_PIPE=1")
		in, e := cmd.StdinPipe()
		if e != nil {
			return chatStartedMsg{err: e}
		}
		pipe, e := cmd.StdoutPipe()
		if e != nil {
			in.Close()
			return chatStartedMsg{err: e}
		}
		dir := filepath.Join(m.cfg.Store.Dir, "logs")
		if e = os.MkdirAll(dir, 0700); e != nil {
			in.Close()
			return chatStartedMsg{err: e}
		}
		log, e := os.OpenFile(filepath.Join(dir, "chat.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if e != nil {
			in.Close()
			return chatStartedMsg{err: e}
		}
		cmd.Stderr = log
		if e = cmd.Start(); e != nil {
			in.Close()
			log.Close()
			return chatStartedMsg{err: e}
		}
		ready := make(chan string, 1)
		done := make(chan error, 1)
		go func() {
			sc := bufio.NewScanner(pipe)
			sc.Buffer(make([]byte, 4096), 4<<20)
			for sc.Scan() {
				var ev struct {
					Type string
					Data struct{ Endpoint string }
				}
				if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Type == "progress" && ev.Data.Endpoint != "" {
					select {
					case ready <- ev.Data.Endpoint:
					default:
					}
				}
			}
			done <- cmd.Wait()
			log.Close()
		}()
		stop := func() error {
			in.Close()
			select {
			case e := <-done:
				return e
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
				return <-done
			}
		}
		select {
		case endpoint := <-ready:
			return chatStartedMsg{client: &upstream.OpenAICompat{BaseURL: endpoint + "/v1", Model: "trialm-chat"}, stop: stop}
		case e := <-done:
			in.Close()
			if e == nil {
				e = fmt.Errorf("chat server stopped before it was ready")
			}
			return chatStartedMsg{err: e}
		case <-m.cfg.Context.Done():
			_ = stop()
			return chatStartedMsg{err: m.cfg.Context.Err()}
		case <-time.After(30 * time.Second):
			_ = stop()
			return chatStartedMsg{err: fmt.Errorf("chat server did not become ready; see chat.log")}
		}
	}
}
func (m *Root) finishChat() tea.Cmd {
	m.busy = true
	stop := m.chatStop
	m.chatStop = nil
	return func() tea.Msg {
		if stop != nil {
			return childMsg{kind: Trial, err: stop()}
		}
		return tea.Quit()
	}
}
