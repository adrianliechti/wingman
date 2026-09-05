package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/abemedia/go-cfb"
	"github.com/adrianliechti/go-extract"

	"github.com/adrianliechti/wingman/pkg/extractor"
)

func TestExtractSupportedFormats(t *testing.T) {
	tests := []struct {
		name      string
		mediaType string
		data      []byte
		want      string
		pages     int
	}{
		{name: "document.pdf", mediaType: "application/pdf", data: buildPDF("PDF integration content"), want: "PDF integration content", pages: 1},
		{name: "document.docx", data: buildDOCX(t, "Word integration content"), want: "Word integration content"},
		{name: "workbook.xlsx", data: buildXLSX(t, "Spreadsheet integration content"), want: "Spreadsheet integration content"},
		{name: "slides.pptx", data: buildPPTX(t, "Presentation integration content"), want: "Presentation integration content", pages: 1},
		{name: "page.html", mediaType: "text/html", data: []byte("<h1>HTML integration content</h1>"), want: "# HTML integration content"},
		{name: "message.eml", mediaType: "message/rfc822", data: buildEML("Email integration content", nil), want: "Email integration content"},
		{name: "message.msg", mediaType: "application/vnd.ms-outlook", data: buildMSG(t, "Outlook integration content"), want: "Outlook integration content"},
		{name: "notes.txt", mediaType: "text/plain", data: []byte("Plain-text integration content"), want: "Plain-text integration content"},
	}

	e, err := New()
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document, err := e.Extract(context.Background(), extractor.File{
				Name:        test.name,
				ContentType: test.mediaType,
				Content:     test.data,
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(document.Text, test.want) {
				t.Fatalf("Text does not contain %q:\n%s", test.want, document.Text)
			}
			if len(document.Pages) != test.pages {
				t.Fatalf("Pages = %d, want %d", len(document.Pages), test.pages)
			}
		})
	}
}

func TestExtractRecursivelyRendersAttachments(t *testing.T) {
	attachment := []byte("Recursive text attachment")
	document, err := newExtractor(t).Extract(context.Background(), extractor.File{
		Name: "message.eml",
		Content: buildEML("Outer message", []mailAttachment{{
			name:      "notes.txt",
			mediaType: "text/plain",
			data:      attachment,
		}}),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"## Attachment contents", "### notes.txt", string(attachment)} {
		if !strings.Contains(document.Text, want) {
			t.Errorf("Text does not contain %q:\n%s", want, document.Text)
		}
	}
}

func TestExtractRejectsUnsupportedAndOCRDocuments(t *testing.T) {
	e := newExtractor(t)

	for _, test := range []struct {
		name string
		file extractor.File
	}{
		{name: "unsupported", file: extractor.File{Name: "data.bin", Content: []byte{0, 1, 2, 3}}},
		{name: "generic headers", file: extractor.File{Content: []byte("Title: Release notes\r\n\r\nEverything is ready.")}},
		{name: "scanned PDF", file: extractor.File{Name: "scan.pdf", Content: buildPDF("")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := e.Extract(context.Background(), test.file, nil)
			if !errors.Is(err, extractor.ErrUnsupported) {
				t.Fatalf("error = %v, want ErrUnsupported", err)
			}
		})
	}
}

func TestNeedsOCRWhenPagesAreMarked(t *testing.T) {
	document := &extract.Document{
		Format:   extract.FormatPDF,
		Metadata: map[string]string{"pages_needing_ocr": "2"},
	}

	if !needsOCR(document, "Usable text from the other pages") {
		t.Fatal("PDF with pages_needing_ocr was accepted")
	}
}

func newExtractor(t *testing.T) *Extractor {
	t.Helper()
	e, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return e
}

type mailAttachment struct {
	name      string
	mediaType string
	data      []byte
}

func buildEML(body string, attachments []mailAttachment) []byte {
	const boundary = "wingman-extract-boundary"
	var message strings.Builder
	fmt.Fprintf(&message, "From: sender@example.com\r\nTo: recipient@example.com\r\nSubject: Integration message\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=%q\r\n\r\n", boundary)
	fmt.Fprintf(&message, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", boundary, body)
	for _, attachment := range attachments {
		fmt.Fprintf(&message, "--%s\r\nContent-Type: %s; name=%q\r\nContent-Disposition: attachment; filename=%q\r\nContent-Transfer-Encoding: base64\r\n\r\n%s\r\n", boundary, attachment.mediaType, attachment.name, attachment.name, base64.StdEncoding.EncodeToString(attachment.data))
	}
	fmt.Fprintf(&message, "--%s--\r\n", boundary)
	return []byte(message.String())
}

func buildDOCX(t *testing.T, content string) []byte {
	return buildArchive(t, map[string]string{
		"[Content_Types].xml": basicContentTypes,
		"_rels/.rels":         rootDocumentRelationship("word/document.xml"),
		"word/document.xml":   `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>` + content + `</w:t></w:r></w:p></w:body></w:document>`,
	})
}

func buildXLSX(t *testing.T, content string) []byte {
	return buildArchive(t, map[string]string{
		"[Content_Types].xml":        basicContentTypes,
		"_rels/.rels":                rootDocumentRelationship("xl/workbook.xml"),
		"xl/workbook.xml":            `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="` + officeRelationshipsNS + `"><sheets><sheet name="Sheet1" sheetId="1" r:id="rSheet"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships xmlns="` + packageRelationshipsNS + `"><Relationship Id="rSheet" Type="` + officeRelationshipsNS + `/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/worksheets/sheet1.xml":   `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>` + content + `</t></is></c></row></sheetData></worksheet>`,
	})
}

func buildPPTX(t *testing.T, content string) []byte {
	return buildArchive(t, map[string]string{
		"[Content_Types].xml":             basicContentTypes,
		"_rels/.rels":                     rootDocumentRelationship("ppt/presentation.xml"),
		"ppt/presentation.xml":            `<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:r="` + officeRelationshipsNS + `"><p:sldIdLst><p:sldId id="256" r:id="rSlide"/></p:sldIdLst></p:presentation>`,
		"ppt/_rels/presentation.xml.rels": `<Relationships xmlns="` + packageRelationshipsNS + `"><Relationship Id="rSlide" Type="` + officeRelationshipsNS + `/slide" Target="slides/slide1.xml"/></Relationships>`,
		"ppt/slides/slide1.xml":           `<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:cSld><p:spTree><p:sp><p:nvSpPr><p:cNvPr id="1" name="Title"/><p:cNvSpPr/><p:nvPr><p:ph type="title"/></p:nvPr></p:nvSpPr><p:txBody><a:p><a:r><a:t>` + content + `</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`,
	})
}

const (
	packageRelationshipsNS = "http://schemas.openxmlformats.org/package/2006/relationships"
	officeRelationshipsNS  = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	basicContentTypes      = `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/></Types>`
)

func rootDocumentRelationship(target string) string {
	return `<Relationships xmlns="` + packageRelationshipsNS + `"><Relationship Id="rDocument" Type="` + officeRelationshipsNS + `/officeDocument" Target="` + target + `"/></Relationships>`
}

func buildArchive(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	for name, content := range parts {
		part, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func buildMSG(t *testing.T, body string) []byte {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "integration-*.msg")
	if err != nil {
		t.Fatal(err)
	}
	writer := cfb.NewWriterV3(file)
	writeCFBStream(t, writer.StorageWriter, "__properties_version1.0", make([]byte, 32))
	writeUnicodeProperty(t, writer.StorageWriter, 0x0037, "Integration message")
	writeUnicodeProperty(t, writer.StorageWriter, 0x1000, body)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeUnicodeProperty(t *testing.T, storage *cfb.StorageWriter, id uint16, value string) {
	t.Helper()
	units := append(utf16.Encode([]rune(value)), 0)
	data := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[i*2:], unit)
	}
	writeCFBStream(t, storage, fmt.Sprintf("__substg1.0_%04X001F", id), data)
}

func writeCFBStream(t *testing.T, storage *cfb.StorageWriter, name string, data []byte) {
	t.Helper()
	stream, err := storage.CreateStream(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
}

func buildPDF(content string) []byte {
	stream := ""
	if content != "" {
		stream = fmt.Sprintf("BT\n/F1 18 Tf\n72 720 Td\n(%s) Tj\nET\n", strings.NewReplacer("\\", "\\\\", "(", "\\(", ")", "\\)").Replace(content))
	}
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}

	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for i, object := range objects {
		offsets[i+1] = pdf.Len()
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for i := 1; i < len(offsets); i++ {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return pdf.Bytes()
}
