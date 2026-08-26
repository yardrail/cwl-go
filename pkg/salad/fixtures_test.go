package salad

// Values shared across the resolution tests, collected so that no literal is
// repeated often enough to be worth a constant of its own.
const (
	testBaseURI = "http://example.com/base"
	testBaseOne = testBaseURI + "#one"
	testAcidNS  = "http://example.com/acid#"
	testAcidRed = testAcidNS + "red"

	testOne   = "one"
	testTwo   = "two"
	testRed   = "red"
	testPlain = "plain"

	docMain   = "main.yml"
	docChild  = "child.yml"
	docSimple = "doc.yml"
	docBody   = "id: doc\n"
	pathABC   = "file:///a/b/c.yml"

	msgPrefixExpands = "a declared prefix expands"
	testAcidSix      = "acid:six"
	testSub          = "sub"
)
