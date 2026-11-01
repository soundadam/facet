package cli

import (
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/spf13/cobra"
)

// Presets and aliases for the two locale fields, so that "Las Vegas",
// "Tokyo" or "中文" are as good as the IANA name or BCP 47 tag.

type zonePreset struct {
	zone string
	// places is shown in hints and `facet zones`; with zh and aliases it is
	// also accepted in place of the zone.
	places  string
	zh      string
	aliases []string
}

var zonePresets = []zonePreset{
	{"America/Los_Angeles", "Las Vegas, Los Angeles, Seattle, San Francisco", "拉斯维加斯 洛杉矶 西雅图 旧金山 美西",
		[]string{"America/Las_Vegas", "Vegas", "LA", "SF", "Pacific", "PST", "PDT", "US West"}},
	{"America/Denver", "Denver, Salt Lake City", "丹佛 盐湖城", []string{"Mountain", "MST"}},
	{"America/Phoenix", "Phoenix (no DST)", "凤凰城", []string{"Phoenix", "Arizona"}},
	{"America/Chicago", "Chicago, Dallas, Houston", "芝加哥 达拉斯 休斯顿", []string{"Central"}},
	{"America/New_York", "New York, Miami, Boston, Washington", "纽约 迈阿密 波士顿 华盛顿 美东",
		[]string{"NYC", "NY", "Eastern", "EST", "EDT", "US East"}},
	{"America/Toronto", "Toronto", "多伦多", nil},
	{"America/Vancouver", "Vancouver", "温哥华", nil},
	{"Europe/London", "London", "伦敦 英国", []string{"UK", "Britain"}},
	{"Europe/Berlin", "Berlin, Frankfurt", "柏林 法兰克福 德国", []string{"Germany"}},
	{"Europe/Paris", "Paris", "巴黎 法国", []string{"France"}},
	{"Asia/Dubai", "Dubai", "迪拜", []string{"UAE"}},
	{"Asia/Singapore", "Singapore", "新加坡", []string{"SG"}},
	{"Asia/Bangkok", "Bangkok", "曼谷 泰国", []string{"Thailand"}},
	{"Asia/Hong_Kong", "Hong Kong", "香港", []string{"HK"}},
	{"Asia/Taipei", "Taipei", "台北 台湾", []string{"Taiwan", "TW"}},
	{"Asia/Shanghai", "Shanghai, Beijing, Shenzhen", "上海 北京 深圳 中国", []string{"China", "CN"}},
	{"Asia/Seoul", "Seoul", "首尔 韩国", []string{"Korea", "KR"}},
	{"Asia/Tokyo", "Tokyo, Osaka", "东京 大阪 日本", []string{"Japan", "JP"}},
	{"Australia/Sydney", "Sydney, Melbourne", "悉尼 墨尔本 澳洲", []string{"Australia"}},
	{"UTC", "Coordinated Universal Time", "", []string{"GMT", "Z"}},
}

type languagePreset struct {
	tag, name string
	aliases   []string
}

var languagePresets = []languagePreset{
	{"en-US", "English (US)", []string{"English", "American", "英语", "英文"}},
	{"en-GB", "English (UK)", []string{"British"}},
	{"zh-CN", "简体中文", []string{"Chinese", "中文", "简体", "简中", "普通话", "Mandarin"}},
	{"zh-TW", "繁體中文（台灣）", []string{"繁体", "繁體", "繁体中文", "繁體中文", "繁中", "Traditional Chinese"}},
	{"zh-HK", "繁體中文（香港）", []string{"粤语", "廣東話", "Cantonese"}},
	{"ja-JP", "日本語", []string{"Japanese", "日语", "日文"}},
	{"ko-KR", "한국어", []string{"Korean", "韩语", "韩文"}},
	{"de-DE", "Deutsch", []string{"German", "德语"}},
	{"fr-FR", "Français", []string{"French", "Francais", "法语"}},
	{"es-ES", "Español", []string{"Spanish", "Espanol", "西班牙语"}},
	{"pt-BR", "Português (Brasil)", []string{"Portuguese", "葡萄牙语"}},
	{"ru-RU", "Русский", []string{"Russian", "俄语"}},
}

var zoneAliases, languageAliases = buildAliases()

func buildAliases() (zones, langs map[string]string) {
	zones, langs = map[string]string{}, map[string]string{}
	for _, p := range zonePresets {
		names := append(strings.Split(p.places, ", "), strings.Fields(p.zh)...)
		for _, n := range append(names, p.aliases...) {
			zones[aliasKey(n)] = p.zone
		}
	}
	for _, p := range languagePresets {
		for _, n := range append([]string{p.name}, p.aliases...) {
			langs[aliasKey(n)] = p.tag
		}
	}
	return zones, langs
}

// aliasKey folds case and drops spaces, underscores and the like, so
// "las vegas", "Las_Vegas" and "LASVEGAS" meet.
func aliasKey(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || strings.ContainsRune("_-.,()（）", r) {
			return -1
		}
		return unicode.ToLower(r)
	}, s)
}

// knownZones are the names whose spelling resolveTimezone corrects:
// macOS loads "asia/tokyo" from its case-insensitive disk, but Chromium
// would not recognise it.
func knownZones() []string {
	zones := slices.Clone(commonZones)
	for _, p := range zonePresets {
		zones = append(zones, p.zone)
	}
	return zones
}

// resolveTimezone maps a zone name, city or country to the IANA name the
// browser is given. "" stays "" (the machine's zone).
func resolveTimezone(in string) (string, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return "", nil
	}
	for _, z := range knownZones() {
		if strings.EqualFold(in, z) {
			return z, nil
		}
	}
	if z, ok := zoneAliases[aliasKey(in)]; ok {
		return z, nil
	}
	if _, err := time.LoadLocation(in); err == nil && (strings.Contains(in, "/") || in == "UTC") {
		return in, nil
	}
	return "", fmt.Errorf(`timezone %q: not an IANA zone name or a known city; try America/New_York, America/Los_Angeles (Las Vegas), Asia/Singapore or Asia/Tokyo, or see "facet zones"`, in)
}

// resolveLanguage maps a BCP 47 tag, in any case and with _ or -, or a
// language name to the tag the browser is given.
func resolveLanguage(in string) (string, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return "", nil
	}
	if tag, ok := languageAliases[aliasKey(in)]; ok {
		return tag, nil
	}
	bad := fmt.Errorf(`language %q: use a BCP 47 tag such as en-US, zh-CN or ja-JP, or a name like English or 中文 (see "facet zones")`, in)
	parts := strings.Split(strings.ReplaceAll(in, "_", "-"), "-")
	for i, s := range parts {
		if len(s) == 0 || len(s) > 8 || strings.IndexFunc(s, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
		}) >= 0 {
			return "", bad
		}
		lower := strings.ToLower(s)
		switch {
		case i == 0:
			if len(s) < 2 || len(s) > 3 || strings.ContainsAny(s, "0123456789") {
				return "", bad
			}
			parts[i] = lower
		case len(s) == 2 && !strings.ContainsAny(s, "0123456789"):
			parts[i] = strings.ToUpper(s) // region: US
		case len(s) == 4 && !strings.ContainsAny(s, "0123456789"):
			parts[i] = strings.ToUpper(lower[:1]) + lower[1:] // script: Hant
		default:
			parts[i] = lower
		}
	}
	return strings.Join(parts, "-"), nil
}

// describeZone is a one-line summary of zone at now: its UTC offset, the
// time there and the places it covers.
func describeZone(zone string, now time.Time) string {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return zone
	}
	t := now.In(loc)
	s := fmt.Sprintf("%s · %s · %s there", zone, t.Format("UTC-07:00"), t.Format("15:04"))
	for _, p := range zonePresets {
		if p.zone == zone && p.zone != "UTC" {
			s += " · " + p.places
		}
	}
	return s
}

func describeLanguage(tag string) string {
	for _, p := range languagePresets {
		if p.tag == tag {
			return tag + " · " + p.name
		}
	}
	return tag
}

func zonePresetValues() []string {
	out := []string{""}
	for _, p := range zonePresets {
		out = append(out, p.zone)
	}
	return out
}

func languagePresetValues() []string {
	out := []string{""}
	for _, p := range languagePresets {
		out = append(out, p.tag)
	}
	return out
}

func (app *App) newZonesCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "zones",
		Aliases: []string{"locales"},
		Short:   "List common time zones and languages for --timezone and --lang",
		GroupID: groupConfig,
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			now := time.Now()
			tw := tabwriter.NewWriter(app.Out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "TIMEZONE\tOFFSET\tNOW\tPLACES")
			for _, p := range zonePresets {
				loc, err := time.LoadLocation(p.zone)
				if err != nil {
					continue
				}
				t := now.In(loc)
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.zone, t.Format("UTC-07:00"), t.Format("15:04"), strings.TrimSpace(p.places+"  "+p.zh))
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			fmt.Fprintln(app.Out)
			tw = tabwriter.NewWriter(app.Out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "LANGUAGE\tNAME")
			for _, p := range languagePresets {
				fmt.Fprintf(tw, "%s\t%s\n", p.tag, p.name)
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			fmt.Fprintln(app.Out, "\nAny IANA zone or BCP 47 tag works too; the cities and names above are accepted as they are,")
			fmt.Fprintln(app.Out, `e.g. facet set 1 --timezone "Las Vegas" --lang 中文`)
			return nil
		},
	}
}
