package ytdlp

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/NamanBalaji/tdm/internal/config"
)

// Format represents a downloadable format as reported by yt-dlp.
type Format struct {
	ID             string  `json:"id"`
	Extension      string  `json:"extension"`
	Resolution     string  `json:"resolution"`
	FormatNote     string  `json:"note"`
	VideoCodec     string  `json:"videoCodec"`
	AudioCodec     string  `json:"audioCodec"`
	AverageBitrate float64 `json:"averageBitrate"`
	Filesize       int64   `json:"filesize"`
	FilesizeApprox int64   `json:"filesizeApprox"`
}

// ListFormats retrieves the available formats for a given URL.
func ListFormats(ctx context.Context, cfg *config.YTDLPConfig, url string) ([]Format, error) {
	if cfg == nil {
		return nil, fmt.Errorf("ytdlp config is required")
	}

	binary := cfg.BinaryPath
	if strings.TrimSpace(binary) == "" {
		binary = "yt-dlp"
	}

	if _, err := exec.LookPath(binary); err != nil {
		return nil, ErrBinaryNotFound
	}

	args := []string{"-J", "--no-playlist", url}
	cmd := exec.CommandContext(ctx, binary, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed != "" {
			return nil, fmt.Errorf("yt-dlp failed to list formats: %s", trimmed)
		}

		return nil, fmt.Errorf("yt-dlp failed to list formats: %w", err)
	}

	var data struct {
		Formats []struct {
			FormatID       string   `json:"format_id"`
			Ext            string   `json:"ext"`
			Resolution     string   `json:"resolution"`
			FormatNote     string   `json:"format_note"`
			VCodec         string   `json:"vcodec"`
			ACodec         string   `json:"acodec"`
			TBR            float64  `json:"tbr"`
			Filesize       *int64   `json:"filesize"`
			FilesizeApprox *int64   `json:"filesize_approx"`
			Width          int      `json:"width"`
			Height         int      `json:"height"`
			FPS            float64  `json:"fps"`
			DynamicRange   string   `json:"dynamic_range"`
			AudioChannels  *int     `json:"audio_channels"`
			Language       string   `json:"language"`
			QualityLabel   string   `json:"quality_label"`
			Format         string   `json:"format"`
			Protocol       string   `json:"protocol"`
			ManifestURL    string   `json:"manifest_url"`
			Fragments      []string `json:"fragments"`
		} `json:"formats"`
	}

	if err := json.Unmarshal(output, &data); err != nil {
		return nil, fmt.Errorf("failed to decode yt-dlp output: %w", err)
	}

	formats := make([]Format, 0, len(data.Formats))
	for _, f := range data.Formats {
		if strings.TrimSpace(f.FormatID) == "" {
			continue
		}

		resolution := strings.TrimSpace(f.Resolution)
		if resolution == "" && f.Width > 0 && f.Height > 0 {
			resolution = fmt.Sprintf("%dx%d", f.Width, f.Height)
		}

		filesize := int64(0)
		if f.Filesize != nil {
			filesize = *f.Filesize
		} else if f.FilesizeApprox != nil {
			filesize = *f.FilesizeApprox
		}

		formats = append(formats, Format{
			ID:             strings.TrimSpace(f.FormatID),
			Extension:      strings.TrimSpace(f.Ext),
			Resolution:     resolution,
			FormatNote:     strings.TrimSpace(f.FormatNote),
			VideoCodec:     strings.TrimSpace(f.VCodec),
			AudioCodec:     strings.TrimSpace(f.ACodec),
			AverageBitrate: f.TBR,
			Filesize:       filesize,
			FilesizeApprox: filesize,
		})
	}

	return formats, nil
}
