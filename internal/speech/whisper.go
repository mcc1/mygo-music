package speech

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var whisperWeights = modelFile{path: "ggml-whisper-large-v3-turbo-zh-TW-q8_0.bin", size: 874188075, sha256: "b97084becc150cde926adfda323d66d89bf8a279eb94662e1bfad1b73ab90dbe"}
var whisperRuntime = modelFile{path: "whisper-bin-x64.zip", size: 3832432, sha256: "b1514ebc099765e39fa37eb780b92a140a94c86bb0b3b3d98226b38825979732"}
var whisperBinaries = []string{"whisper-cli.exe", "whisper.dll", "ggml.dll", "ggml-base.dll", "ggml-cpu.dll"}

func WhisperAvailable() bool { return runtime.GOOS == "windows" && runtime.GOARCH == "amd64" }

func whisperInstalled(root string) bool {
	if !WhisperAvailable() {
		return false
	}
	dir := filepath.Join(root, WhisperModelName)
	info, err := os.Stat(filepath.Join(dir, whisperWeights.path))
	if err != nil || info.Size() != whisperWeights.size {
		return false
	}
	for _, name := range whisperBinaries {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return false
		}
	}
	return true
}

func downloadWhisper(ctx context.Context, root string, progress func(int64, int64)) error {
	if !WhisperAvailable() {
		return ErrUnavailable
	}
	dir := filepath.Join(root, WhisperModelName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	total := Model(WhisperModelName).Size()
	archive := filepath.Join(dir, whisperRuntime.path)
	if digest(archive) != whisperRuntime.sha256 {
		if err := fetch(ctx, "https://github.com/ggml-org/whisper.cpp/releases/download/v1.8.2/"+whisperRuntime.path, archive, whisperRuntime, func(n int64) { progress(n, total) }); err != nil {
			return err
		}
	}
	if err := unpackWhisper(archive, dir); err != nil {
		return err
	}
	weights := filepath.Join(dir, whisperWeights.path)
	if digest(weights) != whisperWeights.sha256 {
		if err := fetch(ctx, "https://huggingface.co/illitan/whisper-large-v3-turbo-zh-TW-ggml/resolve/main/"+whisperWeights.path, weights, whisperWeights, func(n int64) { progress(whisperRuntime.size+n, total) }); err != nil {
			return err
		}
	}
	progress(total, total)
	return nil
}

func unpackWhisper(path, dir string) error {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer archive.Close()
	for _, name := range whisperBinaries {
		entry, err := archive.Open("Release/" + name)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(entry)
		entry.Close()
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0700); err != nil {
			return err
		}
	}
	return nil
}

type Whisper struct {
	language string
	dir      string
	backends []string
}

func NewWhisper(root string) (*Whisper, error) { return NewWhisperWithLanguage(root, "zh") }

func NewWhisperWithLanguage(root, language string) (*Whisper, error) {
	if !whisperInstalled(root) {
		return nil, ErrUnavailable
	}
	dir, err := filepath.Abs(filepath.Join(root, WhisperModelName))
	if err != nil {
		return nil, err
	}
	w := &Whisper{dir: dir, language: language}
	for _, backend := range []string{"cuda", "vulkan"} {
		info, err := os.Stat(filepath.Join(dir, backend, "whisper-cli.exe"))
		if err == nil && info.Mode().IsRegular() {
			w.backends = append(w.backends, backend)
		}
	}
	return w, nil
}

func (*Whisper) Close() {}

func (w *Whisper) Transcribe(ctx context.Context, src Source, from, until time.Duration, said func(Segment), heard func(time.Duration)) error {
	dir, err := os.MkdirTemp("", "mygo-whisper-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	rs := newResampler(src.SampleRate(), src.Channels())
	input := make([]float32, 8192*src.Channels())
	const chunk = 30 * SampleRate
	limit := int64(-1)
	if until > from {
		limit = int64((until - from) * SampleRate / time.Second)
	}
	var offset int64
	var samples []float32
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := src.Read(input)
		samples = rs.push(samples, input[:n])
		if limit >= 0 && int64(len(samples)) > limit-offset {
			samples = samples[:limit-offset]
		}
		end := readErr == io.EOF || limit >= 0 && offset+int64(len(samples)) == limit
		for len(samples) >= chunk || end && len(samples) > 0 {
			count := min(chunk, len(samples))
			start := from + time.Duration(offset)*time.Second/SampleRate
			stop := start + time.Duration(count)*time.Second/SampleRate
			if err := w.transcribeChunk(ctx, dir, samples[:count], start, stop, said); err != nil {
				return err
			}
			offset += int64(count)
			samples = samples[count:]
			heard(stop)
		}
		if end {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func (w *Whisper) transcribeChunk(ctx context.Context, dir string, samples []float32, start, stop time.Duration, said func(Segment)) error {
	const headerSize = 44
	wav := make([]byte, headerSize+len(samples)*2)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], SampleRate)
	binary.LittleEndian.PutUint32(wav[28:], SampleRate*2)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], uint32(len(samples)*2))
	for i, s := range samples {
		pcm := int16(math.Round(float64(max(-1, min(1, s))) * 32767))
		binary.LittleEndian.PutUint16(wav[headerSize+2*i:], uint16(pcm))
	}
	input := filepath.Join(dir, "input.wav")
	if err := os.WriteFile(input, wav, 0600); err != nil {
		return err
	}
	for len(w.backends) > 0 {
		backend := w.backends[0]
		err := w.runChunk(ctx, input, filepath.Join(dir, backend+"-result"), start, stop, backend, said)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		w.backends = w.backends[1:]
		log.Printf("Whisper %s unavailable: %v", backend, err)
	}
	return w.runChunk(ctx, input, filepath.Join(dir, "cpu-result"), start, stop, "cpu", said)
}

func (w *Whisper) runChunk(ctx context.Context, input, output string, start, stop time.Duration, backend string, said func(Segment)) error {
	if backend != "cpu" {
		attempt, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		ctx = attempt
	}
	bin := w.dir
	args := []string{"-m", filepath.Join(w.dir, whisperWeights.path), "-f", input, "-l", w.language, "-t", "4", "-oj", "-of", output}
	if backend == "cpu" {
		args = append(args, "-ng")
	} else {
		bin = filepath.Join(bin, backend)
	}
	if err := os.Remove(output + ".json"); err != nil && !os.IsNotExist(err) {
		return err
	}
	cmd := exec.CommandContext(ctx, filepath.Join(bin, "whisper-cli.exe"), args...)
	cmd.Dir = bin
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	hideWhisperConsole(cmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Whisper: %w", err)
	}
	if backend != "cpu" && !strings.Contains(strings.ToLower(diagnostics.String()), "using "+backend) {
		return fmt.Errorf("Whisper did not activate %s", backend)
	}
	data, err := os.ReadFile(output + ".json")
	if err != nil {
		return err
	}
	var result struct {
		Transcription []struct {
			Text    string
			Offsets struct{ From, To int64 }
		}
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return err
	}
	for _, line := range result.Transcription {
		from := start + time.Duration(max(0, line.Offsets.From))*time.Millisecond
		to := min(stop, start+time.Duration(line.Offsets.To)*time.Millisecond)
		if text := strings.TrimSpace(line.Text); text != "" && to > from {
			said(Segment{Start: from, End: to, Text: text})
		}
	}
	return nil
}
