package cwlcli

import (
	"strings"

	"github.com/yardrail/cwl-go/pkg/salad"
)

// Fetch reads the raw bytes of the document at ref and reports the absolute,
// normalized URL it resolved to.
//
// ref is whatever a user typed: a relative path, an absolute path, a file URL
// or an http(s) URL, optionally with a fragment. Resolution goes through the
// same [salad.Fetcher] the loader uses, so a tool that reads a document itself
// sees exactly the bytes, and reports exactly the URL, that loading it would
// have. A fragment is dropped, on the same terms as cwlcore.LoadFileDocument:
// it selects one object inside a document, and this returns the whole file.
//
// It is for the one job pkg/cwlcore cannot do: parsing a document without
// resolving or validating it, which is what a tool must fall back to when the
// document does not load.
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
