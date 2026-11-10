package plugin

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MoeForge/moejs"
)

// The pinned EJS assets run in a separate Deno process with no permissions.
// Neither the account session nor the signed media URL is passed to it.
//
//go:embed youtube-ejs/yt.solver.lib.min.js
var youtubeSolverLib []byte

//go:embed youtube-ejs/yt.solver.core.min.js
var youtubeSolverCore []byte

var (
	youtubePlayerID           = regexp.MustCompile(`/s/player/([a-f0-9]{8})/`)
	youtubeSignatureTimestamp = regexp.MustCompile(`signatureTimestamp\s*[:=]\s*([0-9]{1,8})`)
	youtubeChallengeValue     = regexp.MustCompile(`^[a-zA-Z0-9_.=-]{1,512}$`)
	errYouTubeSignature       = errors.New("Could not resolve the YouTube Music audio signature")
)

type youtubePlayerScript struct {
	code  string
	stamp int
	until time.Time
}

var youtubeScriptCache struct {
	sync.Mutex
	player youtubePlayerScript
}

var youtubePublicClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func publicYouTubeBytes(ctx context.Context, address string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, errYouTubeSignature
	}
	resp, err := youtubePublicClient.Do(req)
	if err != nil {
		return nil, errYouTubeSignature
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errYouTubeSignature
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil || int64(len(b)) > maxBytes {
		return nil, errYouTubeSignature
	}
	return b, nil
}

func loadYouTubePlayer(ctx context.Context) (youtubePlayerScript, error) {
	cache := &youtubeScriptCache
	cache.Lock()
	defer cache.Unlock()
	if time.Now().Before(cache.player.until) {
		return cache.player, nil
	}
	page, err := publicYouTubeBytes(ctx, "https://www.youtube.com/iframe_api", 64<<10)
	if err != nil {
		return youtubePlayerScript{}, err
	}
	match := youtubePlayerID.FindSubmatch(bytes.ReplaceAll(page, []byte(`\/`), []byte("/")))
	if len(match) != 2 {
		return youtubePlayerScript{}, errYouTubeSignature
	}
	src, err := publicYouTubeBytes(ctx, "https://www.youtube.com/s/player/"+string(match[1])+"/player_ias.vflset/en_US/base.js", 8<<20)
	if err != nil {
		return youtubePlayerScript{}, err
	}
	match = youtubeSignatureTimestamp.FindSubmatch(src)
	if len(match) != 2 {
		return youtubePlayerScript{}, errYouTubeSignature
	}
	stamp, _ := strconv.Atoi(string(match[1]))
	if stamp == 0 {
		return youtubePlayerScript{}, errYouTubeSignature
	}
	cache.player = youtubePlayerScript{code: string(src), stamp: stamp, until: time.Now().Add(20 * time.Minute)}
	return cache.player, nil
}

type youtubeChallenge struct {
	Type       string   `json:"type"`
	Challenges []string `json:"challenges"`
}

func solveYouTubeChallenges(ctx context.Context, script string, requests []youtubeChallenge) ([]string, error) {
	deno, err := exec.LookPath("deno")
	if err != nil {
		return nil, errors.New("Install Deno to play YouTube Music Premium streams")
	}
	input, err := json.Marshal(struct {
		Type     string             `json:"type"`
		Player   string             `json:"player"`
		Requests []youtubeChallenge `json:"requests"`
	}{"player", script, requests})
	if err != nil {
		return nil, errYouTubeSignature
	}
	var program bytes.Buffer
	program.Write(youtubeSolverLib)
	program.WriteString("\nObject.assign(globalThis, lib);\n")
	program.Write(youtubeSolverCore)
	program.WriteString("\nconsole.log(JSON.stringify(jsc(")
	program.Write(input)
	program.WriteString(")));\n")
	cmd := exec.CommandContext(ctx, deno, "run", "--ext=js", "--no-code-cache", "--no-prompt", "--no-remote",
		"--no-lock", "--node-modules-dir=none", "--no-config", "--no-npm", "--cached-only",
		"--deny-net", "--deny-read", "--deny-write", "--deny-run", "--deny-env", "--deny-ffi", "--deny-sys", "-")
	hideConsole(cmd)
	cmd.Env = []string{}
	cmd.Stdin = &program
	// Drain through a bounded pipe: solver output is just a few strings.
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errYouTubeSignature
	}
	if err := cmd.Start(); err != nil {
		return nil, errYouTubeSignature
	}
	const outputLimit = 16 << 10
	raw, readErr := io.ReadAll(io.LimitReader(out, outputLimit+1))
	if len(raw) > outputLimit || readErr != nil {
		_ = cmd.Process.Kill()
	}
	if err := cmd.Wait(); err != nil || readErr != nil || len(raw) > outputLimit {
		return nil, errYouTubeSignature
	}
	var answer struct {
		Type      string `json:"type"`
		Responses []struct {
			Type string            `json:"type"`
			Data map[string]string `json:"data"`
		} `json:"responses"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil || answer.Type != "result" || len(answer.Responses) != len(requests) {
		return nil, errYouTubeSignature
	}
	resolved := make([]string, len(requests))
	for i, r := range answer.Responses {
		value := r.Data[requests[i].Challenges[0]]
		if r.Type != "result" || !youtubeChallengeValue.MatchString(value) || strings.Contains(value, "_w8_") {
			return nil, errYouTubeSignature
		}
		resolved[i] = value
	}
	return resolved, nil
}

func decipherYouTubeAudio(ctx context.Context, stamp int, cipher string) (string, error) {
	if len(cipher) > 16<<10 {
		return "", errYouTubeSignature
	}
	fields, err := url.ParseQuery(cipher)
	if err != nil {
		return "", errYouTubeSignature
	}
	media, err := url.Parse(fields.Get("url"))
	if err != nil || media.Scheme != "https" || media.User != nil || media.Port() != "" || media.Fragment != "" ||
		!strings.HasSuffix(strings.ToLower(media.Hostname()), ".googlevideo.com") || media.Path != "/videoplayback" {
		return "", errYouTubeSignature
	}
	signature, param := fields.Get("s"), fields.Get("sp")
	if !youtubeChallengeValue.MatchString(signature) || (param != "sig" && param != "signature") {
		return "", errYouTubeSignature
	}
	query, err := url.ParseQuery(media.RawQuery)
	if err != nil {
		return "", errYouTubeSignature
	}
	requests := []youtubeChallenge{{Type: "sig", Challenges: []string{signature}}}
	if n := query.Get("n"); n != "" {
		if !youtubeChallengeValue.MatchString(n) {
			return "", errYouTubeSignature
		}
		requests = append(requests, youtubeChallenge{Type: "n", Challenges: []string{n}})
	}
	script, err := loadYouTubePlayer(ctx)
	if err != nil || script.stamp != stamp {
		return "", errYouTubeSignature
	}
	values, err := solveYouTubeChallenges(ctx, script.code, requests)
	if err != nil {
		return "", err
	}
	query.Set(param, values[0])
	if len(values) > 1 {
		query.Set("n", values[1])
	}
	media.RawQuery = query.Encode()
	return media.String(), nil
}

func (p *Plugin) youtubeSignatureTimestampAPI(v *vm) native {
	return func(*moejs.Realm, moejs.Value, []moejs.Value) (moejs.Value, error) {
		promise, settle := v.loop.NewPromise()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			script, err := loadYouTubePlayer(ctx)
			settle(func(rt *moejs.Runtime) (moejs.Value, error) {
				if err != nil {
					return moejs.Undefined(), err
				}
				return rt.FromGo(script.stamp)
			})
		}()
		return promise, nil
	}
}

func (p *Plugin) youtubeDecipherAudioAPI(v *vm) native {
	return func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		stamp, err := strconv.Atoi(moejs.Arg(args, 0).String())
		if err != nil || stamp <= 0 {
			return moejs.Undefined(), errYouTubeSignature
		}
		cipher := moejs.Arg(args, 1).String()
		promise, settle := v.loop.NewPromise()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			address, err := decipherYouTubeAudio(ctx, stamp, cipher)
			settle(func(*moejs.Runtime) (moejs.Value, error) { return moejs.String(address), err })
		}()
		return promise, nil
	}
}
