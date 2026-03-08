package main

import (
	"testing"
	"time"
)

func TestFormatRateLimits_NoData(t *testing.T) {
	title, tooltip := formatRateLimits(0, 0, 0, 0, false, time.Now())
	if title != "N/A" {
		t.Errorf("title = %q, want %q", title, "N/A")
	}
	if tooltip != "what-the-claude" {
		t.Errorf("tooltip = %q, want %q", tooltip, "what-the-claude")
	}
}

func TestFormatRateLimits_5hResetPassed(t *testing.T) {
	now := time.Unix(1000000, 0)
	reset5h := int64(999000)                        // in the past
	reset7d := now.Add(30 * time.Hour).Unix() // 30h in the future

	title, tooltip := formatRateLimits(0.14, 0.02, reset5h, reset7d, true, now)
	if title != "0%" {
		t.Errorf("title = %q, want %q", title, "0%")
	}
	if got := tooltip; got != "5h: 14% (reset passed)\n7d: 2% (reset in 30h)" {
		t.Errorf("tooltip = %q", got)
	}
}

func TestFormatRateLimits_7dResetPassed(t *testing.T) {
	now := time.Unix(1000000, 0)
	reset5h := now.Add(90 * time.Minute).Unix() // exactly 90m, uses half-hour format
	reset7d := int64(999000)                     // in the past

	title, tooltip := formatRateLimits(0.14, 0.02, reset5h, reset7d, true, now)
	if title != "14% 1.5h" {
		t.Errorf("title = %q, want %q", title, "14% 1.5h")
	}
	if got := tooltip; got != "5h: 14% (reset in 01:30)\n7d: 2% (reset passed)" {
		t.Errorf("tooltip = %q", got)
	}
}

func TestFormatRateLimits_BothResetPassed(t *testing.T) {
	now := time.Unix(1000000, 0)
	reset5h := int64(999000)
	reset7d := int64(998000)

	title, tooltip := formatRateLimits(0.50, 0.10, reset5h, reset7d, true, now)
	if title != "0%" {
		t.Errorf("title = %q, want %q", title, "0%")
	}
	if got := tooltip; got != "5h: 50% (reset passed)\n7d: 10% (reset passed)" {
		t.Errorf("tooltip = %q", got)
	}
}

func TestFormatRateLimits_5hMinutes(t *testing.T) {
	tests := []struct {
		name      string
		minutes   int
		wantTitle string
	}{
		{"1 minute", 1, "14% 1m"},
		{"30 minutes", 30, "14% 30m"},
		{"45 minutes", 45, "14% 45m"},
		{"60 minutes", 60, "14% 60m"},
		{"89 minutes", 89, "14% 89m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Unix(1000000, 0)
			reset5h := now.Add(time.Duration(tt.minutes) * time.Minute).Unix()
			reset7d := now.Add(72 * time.Hour).Unix()

			title, _ := formatRateLimits(0.14, 0.02, reset5h, reset7d, true, now)
			if title != tt.wantTitle {
				t.Errorf("title = %q, want %q", title, tt.wantTitle)
			}
		})
	}
}

func TestFormatRateLimits_5hHalfHourRounding(t *testing.T) {
	tests := []struct {
		name      string
		minutes   int
		wantTitle string
	}{
		{"90 minutes = 1.5h", 90, "14% 1.5h"},
		{"105 minutes = 2h", 105, "14% 2h"},
		{"120 minutes = 2h", 120, "14% 2h"},
		{"135 minutes = 2.5h", 135, "14% 2.5h"},
		{"150 minutes = 2.5h", 150, "14% 2.5h"},
		{"165 minutes = 3h", 165, "14% 3h"},
		{"180 minutes = 3h", 180, "14% 3h"},
		{"195 minutes = 3.5h", 195, "14% 3.5h"},
		{"210 minutes = 3.5h", 210, "14% 3.5h"},
		{"225 minutes = 4h", 225, "14% 4h"},
		{"240 minutes = 4h", 240, "14% 4h"},
		{"270 minutes = 4.5h", 270, "14% 4.5h"},
		{"300 minutes = 5h", 300, "14% 5h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Unix(1000000, 0)
			reset5h := now.Add(time.Duration(tt.minutes) * time.Minute).Unix()
			reset7d := now.Add(72 * time.Hour).Unix()

			title, _ := formatRateLimits(0.14, 0.02, reset5h, reset7d, true, now)
			if title != tt.wantTitle {
				t.Errorf("title = %q, want %q", title, tt.wantTitle)
			}
		})
	}
}

func TestFormatRateLimits_5hTooltipResetTime(t *testing.T) {
	now := time.Unix(1000000, 0)
	reset5h := now.Add(3*time.Hour + 15*time.Minute).Unix()
	reset7d := now.Add(72 * time.Hour).Unix()

	_, tooltip := formatRateLimits(0.14, 0.02, reset5h, reset7d, true, now)
	want := "5h: 14% (reset in 03:15)\n7d: 2% (reset in 3d)"
	if tooltip != want {
		t.Errorf("tooltip = %q, want %q", tooltip, want)
	}
}

func TestFormatRateLimits_7dHours(t *testing.T) {
	now := time.Unix(1000000, 0)
	reset5h := now.Add(2 * time.Hour).Unix()

	tests := []struct {
		name        string
		hours       int
		wantContain string
	}{
		{"12 hours", 12, "7d: 2% (reset in 12h)"},
		{"24 hours", 24, "7d: 2% (reset in 24h)"},
		{"47 hours", 47, "7d: 2% (reset in 47h)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset7d := now.Add(time.Duration(tt.hours) * time.Hour).Unix()
			_, tooltip := formatRateLimits(0.14, 0.02, reset5h, reset7d, true, now)
			if got := tooltip; !containsStr(got, tt.wantContain) {
				t.Errorf("tooltip = %q, want to contain %q", got, tt.wantContain)
			}
		})
	}
}

func TestFormatRateLimits_7dDays(t *testing.T) {
	now := time.Unix(1000000, 0)
	reset5h := now.Add(2 * time.Hour).Unix()

	tests := []struct {
		name        string
		hours       int
		wantContain string
	}{
		{"48 hours = 2d", 48, "7d: 2% (reset in 2d)"},
		{"72 hours = 3d", 72, "7d: 2% (reset in 3d)"},
		{"120 hours = 5d", 120, "7d: 2% (reset in 5d)"},
		{"168 hours = 7d", 168, "7d: 2% (reset in 7d)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reset7d := now.Add(time.Duration(tt.hours) * time.Hour).Unix()
			_, tooltip := formatRateLimits(0.14, 0.02, reset5h, reset7d, true, now)
			if got := tooltip; !containsStr(got, tt.wantContain) {
				t.Errorf("tooltip = %q, want to contain %q", got, tt.wantContain)
			}
		})
	}
}

func TestFormatRateLimits_UsagePercentages(t *testing.T) {
	now := time.Unix(1000000, 0)
	reset5h := now.Add(2 * time.Hour).Unix()
	reset7d := now.Add(72 * time.Hour).Unix()

	tests := []struct {
		name          string
		use5h, use7d  float64
		wantTitle5h   string
		wantTooltip5h string
		wantTooltip7d string
	}{
		{"0%", 0.0, 0.0, "0%", "5h: 0%", "7d: 0%"},
		{"14%/2%", 0.14, 0.02, "14%", "5h: 14%", "7d: 2%"},
		{"50%/25%", 0.50, 0.25, "50%", "5h: 50%", "7d: 25%"},
		{"99%/80%", 0.99, 0.80, "99%", "5h: 99%", "7d: 80%"},
		{"100%/100%", 1.0, 1.0, "100%", "5h: 100%", "7d: 100%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, tooltip := formatRateLimits(tt.use5h, tt.use7d, reset5h, reset7d, true, now)
			if !containsStr(title, tt.wantTitle5h) {
				t.Errorf("title = %q, want to contain %q", title, tt.wantTitle5h)
			}
			if !containsStr(tooltip, tt.wantTooltip5h) {
				t.Errorf("tooltip = %q, want to contain %q", tooltip, tt.wantTooltip5h)
			}
			if !containsStr(tooltip, tt.wantTooltip7d) {
				t.Errorf("tooltip = %q, want to contain %q", tooltip, tt.wantTooltip7d)
			}
		})
	}
}

func TestFormatRateLimits_5hBoundaryAt90Minutes(t *testing.T) {
	now := time.Unix(1000000, 0)
	reset7d := now.Add(72 * time.Hour).Unix()

	// 89 minutes should show minutes
	reset5h := now.Add(89 * time.Minute).Unix()
	title, _ := formatRateLimits(0.14, 0.02, reset5h, reset7d, true, now)
	if title != "14% 89m" {
		t.Errorf("89m: title = %q, want %q", title, "14% 89m")
	}

	// 90 minutes should switch to half-hour format
	reset5h = now.Add(90 * time.Minute).Unix()
	title, _ = formatRateLimits(0.14, 0.02, reset5h, reset7d, true, now)
	if title != "14% 1.5h" {
		t.Errorf("90m: title = %q, want %q", title, "14% 1.5h")
	}
}

func TestFormatRateLimits_7dBoundaryAt48Hours(t *testing.T) {
	now := time.Unix(1000000, 0)
	reset5h := now.Add(2 * time.Hour).Unix()

	// 47 hours should show hours
	reset7d := now.Add(47 * time.Hour).Unix()
	_, tooltip := formatRateLimits(0.14, 0.02, reset5h, reset7d, true, now)
	if !containsStr(tooltip, "reset in 47h") {
		t.Errorf("47h: tooltip = %q, want to contain 'reset in 47h'", tooltip)
	}

	// 48 hours should switch to days
	reset7d = now.Add(48 * time.Hour).Unix()
	_, tooltip = formatRateLimits(0.14, 0.02, reset5h, reset7d, true, now)
	if !containsStr(tooltip, "reset in 2d") {
		t.Errorf("48h: tooltip = %q, want to contain 'reset in 2d'", tooltip)
	}
}

func TestFmtTimestamp(t *testing.T) {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 14, 30, 45, 0, now.Location())
	yesterday := today.Add(-24 * time.Hour)

	got := fmtTimestamp(today)
	if got != "14:30:45" {
		t.Errorf("today: got %q, want %q", got, "14:30:45")
	}

	got = fmtTimestamp(yesterday)
	want := yesterday.Format("01/02 15:04:05")
	if got != want {
		t.Errorf("yesterday: got %q, want %q", got, want)
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || findSubstr(s, substr))
}

func findSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
