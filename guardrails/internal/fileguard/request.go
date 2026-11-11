package fileguard

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nonchan7720/manifold/guardrails/internal/extract"
)

// inputFile はリクエストに含まれるファイル1件。
type inputFile struct {
	extract.SourceFile
	// unreadable が空でなければ、中身がリクエストに無いファイル（URL や file_id で
	// 渡されたもの）で、その説明。拒否の理由に使う。
	unreadable string
	// remoteURL は URL で渡されたファイルの URL。許可したホストなら取得して検査する
	// （Config.Fetcher）。
	remoteURL string
}

// 中身がリクエストに無いファイルの説明。
const (
	unreadableImageURL = "images given by URL"
	unreadableFileURL  = "files given by URL"
	unreadableFileID   = "files given by file_id"
)

// requestFiles は req に含まれるファイルを、種類を揃えた一覧にする。
//
//   - images: 画像。base64 か data URL。LiteLLM はメッセージの image_url を
//     そのまま入れるので、http(s) の URL のこともある。
//   - files: Manifold の拡張。画像以外のファイル。
//   - structured_messages: LiteLLM が送るメッセージ。PDF などのファイルは
//     ここの content にしか入らない。画像（image_url）は images にも入るので、
//     ここからは取らない。
//
// URL で渡されたファイルは、許可したホスト（Config.Fetcher）からだけ取得する
// （呼び出し側が指定した URL を取得すると SSRF の入口になる）。それ以外の URL と
// file_id で渡されたファイルは、中身を検査できないファイルとして扱う。
func requestFiles(req map[string]json.RawMessage) ([]inputFile, error) {
	var files []inputFile
	if raw, ok := req[keyImages]; ok {
		var images []string
		if err := json.Unmarshal(raw, &images); err != nil {
			return nil, &badRequestError{"images must be an array of strings"}
		}
		for _, image := range images {
			if isRemoteURL(image) {
				files = append(files, inputFile{unreadable: unreadableImageURL, remoteURL: image})
				continue
			}
			f, err := decodeInline(image)
			if err != nil {
				return nil, &badRequestError{"images must be base64, data URLs or http(s) URLs"}
			}
			files = append(files, inputFile{SourceFile: f})
		}
	}
	if raw, ok := req[keyFiles]; ok {
		var apiFiles []apiFile
		if err := json.Unmarshal(raw, &apiFiles); err != nil {
			return nil, &badRequestError{"files must be an array of {uri, mime_type, data}"}
		}
		for _, f := range apiFiles {
			files = append(files, inputFile{
				URI: f.URI, MIMEType: extract.NormalizeMIME(f.MIMEType, f.Data), Data: f.Data})
		}
	}
	if raw, ok := req[keyStructuredMessages]; ok {
		messageFiles, err := structuredMessageFiles(raw)
		if err != nil {
			return nil, err
		}
		files = append(files, messageFiles...)
	}
	return files, nil
}

// messagePart はメッセージの content の1件。ファイルは OpenAI の Chat Completions
// の形（type: file、file の中に file_data / file_id）と、Responses API の形
// （type: input_file、直下に file_data / file_url / file_id）の両方を読む。
type messagePart struct {
	Type     string       `json:"type"`
	File     *messageFile `json:"file"`
	FileData string       `json:"file_data"`
	FileURL  string       `json:"file_url"`
	FileID   string       `json:"file_id"`
}

type messageFile struct {
	FileData string `json:"file_data"`
	FileURL  string `json:"file_url"`
	FileID   string `json:"file_id"`
}

// structuredMessageFiles は structured_messages の content にあるファイルを返す。
func structuredMessageFiles(raw json.RawMessage) ([]inputFile, error) {
	var messages []struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil, &badRequestError{"structured_messages must be an array of messages"}
	}
	var files []inputFile
	for _, message := range messages {
		// content は文字列か、パートの配列。文字列（や null）にファイルは無い。
		if !strings.HasPrefix(strings.TrimSpace(string(message.Content)), "[") {
			continue
		}
		// 配列なのに読めないものを無視すると、その中のファイルが未検査で通る。
		var parts []messagePart
		if json.Unmarshal(message.Content, &parts) != nil {
			return nil, &badRequestError{"the content of a message must be an array of parts"}
		}
		for _, part := range parts {
			if part.Type != "file" && part.Type != "input_file" {
				continue
			}
			file := messageFile{FileData: part.FileData, FileURL: part.FileURL, FileID: part.FileID}
			if part.File != nil {
				file = *part.File
			}
			switch {
			case file.FileData != "" && !isRemoteURL(file.FileData):
				f, err := decodeInline(file.FileData)
				if err != nil {
					return nil, &badRequestError{"file_data must be base64 or a data URL"}
				}
				files = append(files, inputFile{SourceFile: f})
			case file.FileData != "" || file.FileURL != "":
				remote := file.FileURL
				if file.FileData != "" {
					remote = file.FileData
				}
				files = append(files, inputFile{unreadable: unreadableFileURL, remoteURL: remote})
			default:
				files = append(files, inputFile{unreadable: unreadableFileID})
			}
		}
	}
	return files, nil
}

func isRemoteURL(s string) bool {
	lower := strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// decodeInline は base64、または data URL（data:<種類>;base64,<中身>）をデコードする。
// 種類は data URL の申告と中身から決める。
func decodeInline(s string) (extract.SourceFile, error) {
	declared := ""
	if rest, ok := strings.CutPrefix(s, "data:"); ok {
		meta, payload, found := strings.Cut(rest, ",")
		if !found {
			return extract.SourceFile{}, fmt.Errorf("malformed data URL")
		}
		declared, _, _ = strings.Cut(meta, ";")
		s = payload
	}
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return extract.SourceFile{}, err
	}
	return extract.SourceFile{MIMEType: extract.NormalizeMIME(declared, data), Data: data}, nil
}
