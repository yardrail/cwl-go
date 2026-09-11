package cwlcli

import (
	"strings"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// Fetch reads the raw bytes of the document at ref and returns the normalized URL.
func Fetch(ref string) (_ []byte, _ string, _ error) {
	fetcher := salad.NewDefaultFetcher()
	document, _, _ := strings.Cut(ref, "#")

	url, err := fetcher.Normalize("", document)
	if err != nil {
		return nil, "", err
	}

	src, err := fetcher.FetchText(url)
	if err != nil {
		return nil, url, err
	}

	return src, url, nil
}
