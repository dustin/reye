package vidtool

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

var (
	ffprobe = flag.String("ffprobe", "ffprobe", "path to ffprobe")
	ffmpeg  = flag.String("ffmpeg", "ffmpeg", "path to ffmpeg")
)

func ClipDuration(ctx context.Context, fn string) (time.Duration, error) {
	printfmt := "-print_format"
	if strings.HasSuffix(*ffprobe, "avprobe") {
		printfmt = "-of"
	}
	cmd := exec.CommandContext(ctx, *ffprobe, "-v", "error", printfmt, "json", "-show_format", fn)
	cmd.Stderr = os.Stderr
	o, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	info := struct {
		Format struct {
			Duration string
		}
	}{}

	if err := json.Unmarshal(o, &info); err != nil {
		return 0, err
	}

	return time.ParseDuration(info.Format.Duration + "s")
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func Transcode(ctx context.Context, iname, oname string) (time.Duration, error) {
	idur, err := ClipDuration(ctx, iname)
	if err != nil {
		return 0, err
	}

	cmd := exec.CommandContext(ctx, *ffmpeg, "-y", "-v", "warning", "-i", iname, oname)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		return 0, err
	}

	odur, err := ClipDuration(ctx, oname)
	if err != nil {
		return 0, err
	}

	if abs(odur-idur) > time.Second {
		return 0, fmt.Errorf("durations inconsistent, in=%v, out=%v", idur, odur)
	}

	return odur, nil
}
