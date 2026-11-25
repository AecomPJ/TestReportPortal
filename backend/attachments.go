package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

const mb = 1024 * 1024

// maxAttachmentBytes is the largest per-file limit; per-type limits are applied in classifyAttachment.
const maxAttachmentBytes = 100 * mb

type attachmentKind struct {
	mime  string
	ext   string
	limit int
	label string
}

var (
	kindPNG  = attachmentKind{"image/png", ".png", 10 * mb, "画像"}
	kindJPEG = attachmentKind{"image/jpeg", ".jpg", 10 * mb, "画像"}
	kindWebP = attachmentKind{"image/webp", ".webp", 10 * mb, "画像"}
	kindText = attachmentKind{"text/plain; charset=utf-8", ".txt", 20 * mb, "テキスト"}
	kindJSON = attachmentKind{"application/json", ".json", 20 * mb, "JSON"}
	kindXML  = attachmentKind{"application/xml", ".xml", 20 * mb, "XML"}
	kindZIP  = attachmentKind{"application/zip", ".zip", 50 * mb, "ZIP"}
	kindMP4  = attachmentKind{"video/mp4", ".mp4", 100 * mb, "動画"}
)

const unsupportedAttachment = "対応形式は画像(PNG/JPEG/WebP)、テキスト(.txt/.log)、JSON、XML、ZIP、MP4です"

func isInlineImage(typ string) bool {
	return typ == kindPNG.mime || typ == kindJPEG.mime || typ == kindWebP.mime
}

// classifyAttachment decides the stored MIME type from the content, using the file extension only to
// pick between text formats. data may be up to maxAttachmentBytes+1 long.
func classifyAttachment(name string, data []byte) (string, string, error) {
	if len(data) == 0 {
		return "", "", echo.NewHTTPError(400, "空のファイルは添付できません")
	}
	sniffed := http.DetectContentType(data)
	ext := strings.ToLower(filepath.Ext(name))
	// Any text sniff is accepted for text formats; the stored type is never text/html and downloads are not rendered.
	textual := strings.HasPrefix(sniffed, "text/")
	var k attachmentKind
	switch {
	case sniffed == kindPNG.mime:
		k = kindPNG
	case sniffed == kindJPEG.mime:
		k = kindJPEG
	case sniffed == kindWebP.mime:
		k = kindWebP
	case sniffed == kindZIP.mime && ext == ".zip":
		k = kindZIP
	case sniffed == kindMP4.mime && ext == ".mp4":
		k = kindMP4
	case textual && ext == ".json":
		k = kindJSON
	case textual && ext == ".xml":
		k = kindXML
	case textual && (ext == ".txt" || ext == ".log"):
		k = kindText
		// Keep the charset the sniffer detected (e.g. UTF-16 with a BOM): the bytes are stored as-is,
		// so the served Content-Type must describe them. Other text sniffs such as text/html stay text/plain.
		if strings.HasPrefix(sniffed, "text/plain;") {
			k.mime = sniffed
		}
	default:
		return "", "", echo.NewHTTPError(400, unsupportedAttachment)
	}
	if len(data) > k.limit {
		return "", "", echo.NewHTTPError(413, fmt.Sprintf("%sは%dMB以内にしてください", k.label, k.limit/mb))
	}
	if k == kindJSON && !json.Valid(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))) {
		return "", "", echo.NewHTTPError(400, "JSONの形式が不正です")
	}
	return k.mime, k.ext, nil
}

// Attachment uploads get a larger body limit than the global JSON limit.
func isAttachmentUpload(c echo.Context) bool {
	return c.Request().Method == http.MethodPost && strings.HasSuffix(c.Request().URL.Path, "/attachments")
}

var attachmentBodyLimit = middleware.BodyLimit("101M")
