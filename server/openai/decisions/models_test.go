package decisions

import (
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
)

func TestRequestValidation(t *testing.T) {
	validQuestions := `[{"type":"predicate","instructions":""}]`
	for _, input := range []string{
		`""`, `"hello"`,
		`[{"role":"user","content":"hello"}]`,
		`[{"role":"assistant","content":"hello"}]`,
		`[{"type":"message","role":"user","content":[{"type":"input_text","text":""},{"type":"input_image","image_url":"data:image/png;base64,aGk=","detail":"original"}]}]`,
		`[{"type":"function_call","call_id":"c1","name":"fn","arguments":"{}"},{"type":"function_call_output","call_id":"c1","output":[{"type":"input_image","image_url":"data:image/png;base64,aGk="}]}]`,
	} {
		var r Request
		if err := json.Unmarshal([]byte(fmt.Sprintf(`{"model":"m","input":%s,"questions":%s}`, input, validQuestions)), &r); err != nil {
			t.Fatal(err)
		}
		if _, err := r.ProviderInput(); err != nil {
			t.Fatalf("input %s: %v", input, err)
		}
	}
	for _, input := range []string{
		`null`, `{}`, `true`, `[]`,
		`[{"role":"user","content":[{"type":"input_image","image_url":"ftp://example.com/image.png"}]}]`,
		`[{"role":"user","content":null}]`,
		`[{"role":"user","content":[{"type":"input_file","file_id":"f"}]}]`,
		`[{"role":"user","content":[{"type":"input_text"}]}]`,
		`[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,?","detail":"high"}]}]`,
		`[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,aGk=","detail":"invalid"}]}]`,
	} {
		var r Request
		if err := json.Unmarshal([]byte(fmt.Sprintf(`{"model":"m","input":%s,"questions":%s}`, input, validQuestions)), &r); err != nil {
			t.Fatal(err)
		}
		if _, err := r.ProviderInput(); err == nil {
			t.Fatalf("accepted invalid input %s", input)
		}
	}
	image := `{"type":"input_image","image_url":"data:image/png;base64,aGk="}`
	var r Request
	json.Unmarshal([]byte(fmt.Sprintf(`{"model":"m","input":[{"role":"user","content":[%s]}],"questions":%s}`, strings.TrimSuffix(strings.Repeat(image+",", 129), ","), validQuestions)), &r)
	if _, err := r.ProviderInput(); err == nil {
		t.Fatal("accepted 129 images")
	}
	for _, questions := range []string{
		`[]`, `[{"type":"predicate"}]`,
		`[{"type":"predicate","instructions":null}]`,
		`[{"type":"other","instructions":""}]`,
		`[{"type":"choice","instructions":"","choices":[{"value":1}]}]`,
		`[{"type":"choice","instructions":"","choices":[{"value":"a"},{"value":"a"}]}]`,
		`[{"type":"score","instructions":"","levels":[]}]`,
	} {
		json.Unmarshal([]byte(fmt.Sprintf(`{"model":"m","input":"hello","questions":%s}`, questions)), &r)
		if _, err := r.ProviderInput(); err == nil {
			t.Fatalf("accepted invalid questions %s", questions)
		}
	}
}
