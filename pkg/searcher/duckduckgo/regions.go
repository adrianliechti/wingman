package duckduckgo

// Country codes mapped to the documented kl regions. For multilingual regions,
// use the first documented locale as the country default.
// https://duckduckgo.com/duckduckgo-help-pages/settings/params
var regions = map[string]string{
	"AR": "ar-es", "AT": "at-de", "AU": "au-en", "BE": "be-fr",
	"BG": "bg-bg", "BR": "br-pt", "CA": "ca-en", "CH": "ch-de",
	"CL": "cl-es", "CN": "cn-zh", "CO": "co-es", "CZ": "cz-cs",
	"DE": "de-de", "DK": "dk-da", "EE": "ee-et", "ES": "es-es",
	"FI": "fi-fi", "FR": "fr-fr", "GB": "uk-en", "GR": "gr-el",
	"HK": "hk-tzh", "HR": "hr-hr", "HU": "hu-hu", "ID": "id-id",
	"IE": "ie-en", "IL": "il-he", "IN": "in-en", "IT": "it-it",
	"JP": "jp-jp", "KR": "kr-kr", "LT": "lt-lt", "LV": "lv-lv",
	"MX": "mx-es", "MY": "my-ms", "NL": "nl-nl", "NO": "no-no",
	"NZ": "nz-en", "PE": "pe-es", "PH": "ph-en", "PL": "pl-pl",
	"PT": "pt-pt", "RO": "ro-ro", "RU": "ru-ru", "SE": "se-sv",
	"SG": "sg-en", "SI": "sl-sl", "SK": "sk-sk", "TH": "th-th",
	"TR": "tr-tr", "TW": "tw-tzh", "UA": "ua-uk", "US": "us-en",
	"VE": "ve-es", "VN": "vn-vi", "ZA": "za-en",
}
