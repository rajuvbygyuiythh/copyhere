// apitest — standalone console chat tester for an OpenAI-compatible API
// (Featherless, OpenAI, Groq, OpenRouter, Gemini, DeepSeek ...).
//
// It is deliberately NOT connected to the main ChitChat Bot code: one file,
// no dependencies, its own go.mod. Use it to check that the API works and to
// see / tune how the AI persona chats before anything is wired into the bot.
//
//	apitest.exe            interactive chat (you are the stranger)
//	apitest.exe ping       one raw request, prints status + body
//
// Settings: apitest.config.txt (BASE_URL / MODEL / API_KEY / ...).
// Persona:  persona.txt (system prompt; {age} {country} are random per /new).
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type config struct {
	BaseURL     string
	Model       string
	APIKey      string
	Timeout     time.Duration
	MaxTokens   int
	Temperature float64
	History     int
	AgeMin      int
	AgeMax      int
	Countries   []string
	ShowPrompt  bool
	// ExtraJSON is merged into every request body, e.g. to disable thinking:
	//   {"chat_template_kwargs":{"enable_thinking":false}}
	ExtraJSON map[string]any
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type session struct {
	Age     int
	Country string
	System  string
	History []message
	Turns   int
}

var (
	thinkRe     = regexp.MustCompile(`(?s)<think>.*?</think>`)
	forbiddenRe = regexp.MustCompile(`(?i)(@|https?://|www\.|as an ai|language model|i am a bot|i'm a bot)`)
)

func main() {
	dir := exeDir()
	cfg, err := loadConfig(filepath.Join(dir, "apitest.config.txt"))
	if err != nil {
		fatal(err)
	}
	personaTemplate, err := loadPersona(filepath.Join(dir, "persona.txt"))
	if err != nil {
		fatal(err)
	}

	mode := "chat"
	if len(os.Args) > 1 {
		mode = strings.ToLower(os.Args[1])
	}
	switch mode {
	case "ping":
		ping(cfg)
	case "chat":
		chatLoop(cfg, personaTemplate)
	default:
		fmt.Println("usage: apitest.exe [chat|ping]")
		os.Exit(2)
	}
}

// ---------------------------------------------------------------- ping ---

func ping(cfg config) {
	fmt.Printf("POST %s/chat/completions  model=%s\n", cfg.BaseURL, cfg.Model)
	start := time.Now()
	status, body, err := rawCall(cfg, []message{
		{Role: "system", Content: "reply with exactly: pong"},
		{Role: "user", Content: "ping"},
	}, 20)
	fmt.Printf("took %.1fs\n", time.Since(start).Seconds())
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
	fmt.Println("HTTP", status)
	fmt.Println(prettyJSON(body))
	var parsed chatResponse
	if json.Unmarshal(body, &parsed) == nil && len(parsed.Choices) > 0 {
		said := strings.TrimSpace(parsed.Choices[0].Message.Content)
		fmt.Printf("\nmodel said: %q\n", said)
		if said == "" {
			fmt.Println("RESULT: API OK but EMPTY reply -> reasoning model used all tokens thinking.")
			fmt.Println("        -> set MAX_TOKENS=1024 or EXTRA_JSON to disable thinking, or pick a non-reasoning model")
			return
		}
		fmt.Println("RESULT: API OK")
		return
	}
	if status >= 400 {
		msg := "see body above"
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		fmt.Printf("RESULT: API ERROR HTTP %d - %s\n", status, msg)
		switch status {
		case 401, 403:
			fmt.Println("        -> API_KEY wrong / missing (apitest.config.txt)")
		case 404:
			fmt.Println("        -> BASE_URL or MODEL id wrong")
		case 429:
			fmt.Println("        -> rate / concurrency limit of your plan - wait and retry")
		}
		os.Exit(1)
	}
	fmt.Println("RESULT: API answered but no choices -> check model id")
	os.Exit(1)
}

// ---------------------------------------------------------------- chat ---

func chatLoop(cfg config, personaTemplate string) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	s := newSession(cfg, personaTemplate, rng)
	printBanner(cfg, s)

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 64*1024)
	for {
		fmt.Print("you> ")
		if !in.Scan() {
			return
		}
		text := strings.TrimSpace(in.Text())
		if text == "" {
			continue
		}
		switch strings.ToLower(text) {
		case "/quit", "/exit", "/q":
			return
		case "/new":
			s = newSession(cfg, personaTemplate, rng)
			fmt.Printf("--- new chat: persona f %d from %s ---\n", s.Age, s.Country)
			continue
		case "/prompt":
			cfg.ShowPrompt = !cfg.ShowPrompt
			fmt.Printf("--- show prompt: %v ---\n", cfg.ShowPrompt)
			continue
		case "/system":
			fmt.Println("--- system prompt ---")
			fmt.Println(s.System)
			fmt.Println("---------------------")
			continue
		case "/history":
			for i, m := range s.History {
				fmt.Printf("%2d %-9s %s\n", i+1, m.Role+":", m.Content)
			}
			continue
		case "/help":
			printHelp()
			continue
		}

		s.History = append(s.History, message{Role: "user", Content: text})
		msgs := buildMessages(s, cfg.History)
		if cfg.ShowPrompt {
			fmt.Println("--- request messages ---")
			for _, m := range msgs {
				fmt.Printf("[%s] %s\n", m.Role, m.Content)
			}
			fmt.Println("------------------------")
		}

		start := time.Now()
		reply, info, err := complete(cfg, msgs)
		took := time.Since(start).Seconds()
		if err != nil {
			fmt.Printf("eva> (no reply)\n     ERROR after %.1fs: %v\n", took, err)
			// keep the user line so the next turn still has context
			continue
		}
		clean, warn := sanitize(reply)
		s.History = append(s.History, message{Role: "assistant", Content: clean})
		s.Turns++
		fmt.Printf("eva> %s\n", clean)
		fmt.Printf("     [%.1fs%s] turn %d | persona f %d %s", took, info, s.Turns, s.Age, s.Country)
		if warn != "" {
			fmt.Printf(" | WARN: %s", warn)
		}
		if clean != strings.TrimSpace(reply) {
			fmt.Printf("\n     raw: %q", strings.TrimSpace(reply))
		}
		fmt.Println()
	}
}

func newSession(cfg config, personaTemplate string, rng *rand.Rand) *session {
	s := &session{
		Age:     cfg.AgeMin + rng.Intn(cfg.AgeMax-cfg.AgeMin+1),
		Country: cfg.Countries[rng.Intn(len(cfg.Countries))],
	}
	sys := strings.ReplaceAll(personaTemplate, "{age}", strconv.Itoa(s.Age))
	sys = strings.ReplaceAll(sys, "{country}", s.Country)
	s.System = sys
	return s
}

func buildMessages(s *session, history int) []message {
	msgs := []message{{Role: "system", Content: s.System}}
	h := s.History
	if history > 0 && len(h) > history {
		h = h[len(h)-history:]
	}
	return append(msgs, h...)
}

func complete(cfg config, msgs []message) (string, string, error) {
	status, body, err := rawCall(cfg, msgs, cfg.MaxTokens)
	if err != nil {
		return "", "", err
	}
	var parsed chatResponse
	if jsonErr := json.Unmarshal(body, &parsed); jsonErr != nil {
		return "", "", fmt.Errorf("HTTP %d, non-JSON body: %s", status, trim(string(body), 300))
	}
	if status >= 400 {
		msg := trim(string(body), 300)
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		return "", "", fmt.Errorf("HTTP %d: %s", status, msg)
	}
	if len(parsed.Choices) == 0 {
		return "", "", errors.New("no choices in response")
	}
	info := ""
	if parsed.Usage != nil {
		info = fmt.Sprintf(", %d+%d tok", parsed.Usage.PromptTokens, parsed.Usage.CompletionTokens)
	}
	choice := parsed.Choices[0]
	if fr := choice.FinishReason; fr != "" && fr != "stop" {
		info += ", finish=" + fr
	}
	content := choice.Message.Content
	if strings.TrimSpace(thinkRe.ReplaceAllString(content, "")) == "" {
		reasoning := strings.TrimSpace(choice.Message.ReasoningContent)
		if reasoning == "" {
			reasoning = strings.TrimSpace(content)
		}
		hint := "model spent all tokens THINKING and never wrote the reply"
		if reasoning != "" {
			hint += fmt.Sprintf(" (reasoning %d chars: %q)", len(reasoning), trim(reasoning, 160))
		}
		hint += "\n     FIX: raise MAX_TOKENS (e.g. 1024) or disable thinking with EXTRA_JSON in apitest.config.txt, or use a non-reasoning model (e.g. meta-llama/Llama-3.3-70B-Instruct)"
		return "", "", errors.New(hint)
	}
	return content, info, nil
}

func rawCall(cfg config, msgs []message, maxTokens int) (int, []byte, error) {
	fields := map[string]any{
		"model":       cfg.Model,
		"messages":    msgs,
		"max_tokens":  maxTokens,
		"temperature": cfg.Temperature,
	}
	for k, v := range cfg.ExtraJSON {
		fields[k] = v
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		return 0, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	resp, err := (&http.Client{Timeout: cfg.Timeout}).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

// sanitize mirrors the rules the real bot would enforce, so you can see
// whether the model output would pass or fall back.
func sanitize(raw string) (string, string) {
	text := thinkRe.ReplaceAllString(raw, "")
	text = strings.TrimSpace(text)
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	text = strings.Trim(text, "\"'“”`")
	var warns []string
	if text == "" {
		return "(empty)", "empty reply -> bot would use a TXT fallback line"
	}
	if len([]rune(text)) > 160 {
		warns = append(warns, "too long (>160 chars) -> bot would fall back")
	}
	if forbiddenRe.MatchString(text) {
		warns = append(warns, "contains @ / link / ai-talk -> bot would fall back")
	}
	if strings.Contains(raw, "<think>") {
		warns = append(warns, "model emitted <think> reasoning (stripped)")
	}
	return strings.ToLower(text), strings.Join(warns, "; ")
}

// -------------------------------------------------------------- config ---

func loadConfig(path string) (config, error) {
	cfg := config{
		BaseURL:     "https://api.featherless.ai/v1",
		Model:       "deepseek-ai/DeepSeek-V4.1-Flash",
		Timeout:     60 * time.Second,
		MaxTokens:   80,
		Temperature: 0.9,
		History:     30,
		AgeMin:      20,
		AgeMax:      25,
		Countries:   []string{"uk", "germany", "france", "italy", "spain", "netherlands", "sweden", "poland", "ireland", "norway"},
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, fmt.Errorf("%s not found (apitest.bat creates it) - fill in API_KEY first", filepath.Base(path))
		}
		return cfg, err
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		key = strings.ToUpper(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "BASE_URL":
			cfg.BaseURL = strings.TrimRight(value, "/")
		case "MODEL":
			cfg.Model = value
		case "API_KEY":
			cfg.APIKey = value
		case "TIMEOUT_SEC":
			if n, err := strconv.Atoi(value); err == nil && n > 0 {
				cfg.Timeout = time.Duration(n) * time.Second
			}
		case "MAX_TOKENS":
			if n, err := strconv.Atoi(value); err == nil && n > 0 {
				cfg.MaxTokens = n
			}
		case "TEMPERATURE":
			if f, err := strconv.ParseFloat(value, 64); err == nil {
				cfg.Temperature = f
			}
		case "HISTORY":
			if n, err := strconv.Atoi(value); err == nil && n >= 0 {
				cfg.History = n
			}
		case "AGE_MIN":
			if n, err := strconv.Atoi(value); err == nil && n >= 18 {
				cfg.AgeMin = n
			}
		case "AGE_MAX":
			if n, err := strconv.Atoi(value); err == nil && n >= 18 {
				cfg.AgeMax = n
			}
		case "EXTRA_JSON":
			var extra map[string]any
			if err := json.Unmarshal([]byte(value), &extra); err != nil {
				return cfg, fmt.Errorf("EXTRA_JSON is not valid JSON: %v", err)
			}
			cfg.ExtraJSON = extra
		case "COUNTRIES":
			var list []string
			for _, c := range strings.Split(value, ",") {
				if c = strings.TrimSpace(strings.ToLower(c)); c != "" {
					list = append(list, c)
				}
			}
			if len(list) > 0 {
				cfg.Countries = list
			}
		}
	}
	if cfg.AgeMax < cfg.AgeMin {
		cfg.AgeMax = cfg.AgeMin
	}
	if cfg.APIKey == "" || strings.Contains(cfg.APIKey, "your-") {
		return cfg, errors.New("API_KEY is empty in apitest.config.txt - paste your key and run again")
	}
	if cfg.Model == "" || cfg.BaseURL == "" {
		return cfg, errors.New("MODEL / BASE_URL must not be empty")
	}
	return cfg, nil
}

func loadPersona(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("persona.txt: %w", err)
	}
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		lines = append(lines, strings.TrimRight(line, " "))
	}
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	if text == "" {
		return "", errors.New("persona.txt is empty")
	}
	return text, nil
}

// --------------------------------------------------------------- misc ---

func printBanner(cfg config, s *session) {
	fmt.Println("==========================================================")
	fmt.Println(" apitest - chat with the AI persona (you are the stranger)")
	fmt.Printf(" api:     %s\n model:   %s\n", cfg.BaseURL, cfg.Model)
	fmt.Printf(" persona: f %d from %s   (persona.txt, /new = re-roll)\n", s.Age, s.Country)
	fmt.Println(" type a message, or /help for commands")
	fmt.Println("==========================================================")
}

func printHelp() {
	fmt.Println(" /new      new chat with a new random persona")
	fmt.Println(" /system   show the current system prompt")
	fmt.Println(" /prompt   toggle printing the full request before each call")
	fmt.Println(" /history  show the conversation memory sent to the model")
	fmt.Println(" /quit     exit")
}

func exeDir() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	wd, _ := os.Getwd()
	return wd
}

func prettyJSON(b []byte) string {
	var out bytes.Buffer
	if json.Indent(&out, b, "", "  ") != nil {
		return trim(string(b), 2000)
	}
	return trim(out.String(), 4000)
}

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func fatal(err error) {
	fmt.Println("ERROR:", err)
	os.Exit(1)
}
