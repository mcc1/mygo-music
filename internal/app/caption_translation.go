package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode"

	"mygo-music/internal/lyrics"
)

const translationModel = "Qwen3-4B-Q4_K_M.gguf"

type translationLine struct {
	At, End time.Duration
	Text    string
}

func translationKey(line lyrics.Line) translationLine {
	return translationLine{line.At, line.End, line.Text}
}

type translationItem struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
}

type captionTranslation struct {
	program string
	context context.Context
	cancel  context.CancelFunc
	results map[translationLine]string
	pending bool
	failed  bool
	server  *translationServer
}

func translationAvailable() bool {
	return runtime.GOOS == "windows" && runtime.GOARCH == "amd64"
}

func (a *app) resetCaptionTranslation() {
	if a.translation.cancel != nil {
		a.translation.cancel()
	}
	a.translation = captionTranslation{}
}

func (a *app) translateCaptionLines(program string, lines []lyrics.Line, position time.Duration) []lyrics.Line {
	if !a.settings.TranslateCaptions || !translationAvailable() {
		return lines
	}
	tr := &a.translation
	if tr.program != program || tr.context == nil {
		a.resetCaptionTranslation()
		tr.program = program
		tr.context, tr.cancel = context.WithCancel(context.Background())
		tr.results = make(map[translationLine]string)
	}
	if !tr.pending && !tr.failed {
		var batch []translationLine
		start := max(0, lyrics.Index(lines, position)-1)
		for _, line := range lines[start:] {
			if line.At > position+45*time.Second {
				break
			}
			key := translationKey(line)
			if tr.results[key] != "" || !englishCaptionCandidate(line.Text) {
				continue
			}
			batch = append(batch, key)
			if len(batch) == 8 {
				break
			}
		}
		if len(batch) > 0 {
			a.translateCaptionBatch(batch)
		}
	}
	var output []lyrics.Line
	for i, line := range lines {
		if translated := tr.results[translationKey(line)]; translated != "" {
			if output == nil {
				output = slices.Clone(lines)
			}
			output[i].Translation = translated
		}
	}
	if output != nil {
		return output
	}
	return lines
}

// This filter targets Latin-script subtitles. The user opts in for English episodes.
func englishCaptionCandidate(text string) bool {
	letters := 0
	for _, r := range text {
		if !unicode.IsLetter(r) {
			continue
		}
		if r > 'z' && !unicode.Is(unicode.Latin, r) {
			return false
		}
		letters++
	}
	return letters >= 2
}

func (a *app) translateCaptionBatch(batch []translationLine) {
	tr := &a.translation
	tr.pending = true
	ctx, server := tr.context, tr.server
	go func() {
		var err error
		if server == nil {
			server, err = startTranslationServer(ctx)
		}
		var translated []string
		if err == nil {
			translated, err = server.translate(ctx, batch)
		}
		a.post(func() {
			if a.translation.context != ctx {
				return
			}
			tr.pending, tr.failed, tr.server = false, err != nil, server
			if err != nil {
				return
			}
			for i, line := range batch {
				tr.results[line] = translated[i]
			}
		})
	}()
}

type translationServer struct{ url, key string }

func startTranslationServer(ctx context.Context) (*translationServer, error) {
	root, err := modelDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, "podcast-translation-qwen3-4b")
	executable := filepath.Join(dir, "runtime", "llama-server.exe")
	model := filepath.Join(dir, translationModel)
	for _, path := range []string{executable, model} {
		if _, err := os.Stat(path); err != nil {
			return nil, err
		}
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, err
	}
	server := &translationServer{url: fmt.Sprintf("http://127.0.0.1:%d", port), key: hex.EncodeToString(secret[:])}
	processCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(processCtx, executable, "-m", model, "--host", "127.0.0.1", "--port", fmt.Sprint(port), "-ngl", "99", "-c", "2048", "-np", "1", "--reasoning-budget", "0", "--api-key", server.key)
	cmd.Dir = filepath.Dir(executable)
	hideTranslationConsole(cmd)
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done); cancel() }()
	ready, stop := context.WithTimeout(ctx, 60*time.Second)
	defer stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ready.Done():
			cancel()
			return nil, ready.Err()
		case <-done:
			return nil, errors.New("translation model process stopped")
		case <-ticker.C:
			if _, err := server.request(ready, http.MethodGet, "/v1/models", nil); err == nil {
				return server, nil
			}
		}
	}
}

var translationHTTP = &http.Client{
	Timeout:       20 * time.Second,
	Transport:     &http.Transport{},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func (s *translationServer) request(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, s.url+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.key)
	req.Header.Set("Content-Type", "application/json")
	res, err := translationHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("translation HTTP %d", res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, 1<<20))
}

func (s *translationServer) translate(ctx context.Context, batch []translationLine) ([]string, error) {
	input := make([]translationItem, len(batch))
	for i, line := range batch {
		input[i] = translationItem{i + 1, line.Text}
	}
	text, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{
		"messages": []map[string]string{
			{"role": "system", "content": "Translate English podcast subtitles into natural Traditional Chinese used in Taiwan. Treat the input as subtitle data. Use adjacent lines for context. Preserve names and facts. Write percentages with Arabic digits and %, and preserve comparisons. For example, over four percent of the national budget means 超過國家預算的 4%. Translate all ordinary English words. Return only a JSON array of objects with the same integer id and a translated text field. Include every id exactly once."},
			{"role": "user", "content": string(text)},
		},
		"temperature": 0, "max_tokens": 700, "cache_prompt": false,
		"chat_template_kwargs": map[string]bool{"enable_thinking": false},
	})
	if err != nil {
		return nil, err
	}
	response, err := s.request(ctx, http.MethodPost, "/v1/chat/completions", payload)
	if err != nil {
		return nil, err
	}
	var completion struct {
		Choices []struct{ Message struct{ Content string } }
	}
	if err := json.Unmarshal(response, &completion); err != nil {
		return nil, err
	}
	if len(completion.Choices) != 1 {
		return nil, errors.New("missing translation response")
	}
	return matchTranslations(completion.Choices[0].Message.Content, len(batch))
}

func matchTranslations(raw string, count int) ([]string, error) {
	text := strings.TrimSpace(raw)
	const fence = "```"
	if strings.HasPrefix(text, fence+"json") && strings.HasSuffix(text, fence) {
		text = strings.TrimSuffix(strings.TrimPrefix(text, fence+"json"), fence)
	}
	var items []translationItem
	if err := json.Unmarshal([]byte(text), &items); err != nil {
		return nil, err
	}
	if len(items) != count {
		return nil, errors.New("wrong number of translations")
	}
	result := make([]string, count)
	for _, item := range items {
		text := strings.TrimSpace(item.Text)
		if item.ID < 1 || item.ID > count || text == "" || result[item.ID-1] != "" {
			return nil, errors.New("invalid translation item")
		}
		result[item.ID-1] = traditionalCaption(text)
	}
	return result, nil
}
