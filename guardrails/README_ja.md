# manifold-guardrail

[English](README.md)

`manifold-guardrail` は、[LiteLLM の Generic Guardrail API](https://docs.litellm.ai/docs/adding_provider/generic_guardrail_api) を話すガードレールのアダプタです。[Manifold](../README_ja.md) の `guardrail.url` の先にも、LiteLLM のガードレールとしても置けます。

> [!WARNING]
> **実験的（0.x）です。** フラグや挙動は予告なく変わることがあります。Bedrock / Google / Azure との結合は、公式ドキュメントと SDK の型に合わせて実装し、偽のクライアントでテストしていますが、実際のサービスに対しては動かしていません。導入時に実機で確認してください。

Manifold 本体とは別の Go モジュール・別のバイナリ・別のイメージで、バージョンも本体とは独立しています。LLM や OCR の SDK の依存（とその不具合・脆弱性）を本体に持ち込まないためです。

リリースは `guardrails/vX.Y.Z` というタグで出ます（本体の `vX.Y.Z` とは別系統です）。

- **イメージ**: `ghcr.io/nonchan7720/manifold-guardrail:vX.Y.Z`（linux/amd64、linux/arm64）
- **バイナリ**: GitHub のリリース `guardrails/vX.Y.Z` に、OS・アーキテクチャごとのアーカイブと `checksums.txt` を添付します。

手元でビルドする場合:

```bash
docker build -t manifold-guardrail .      # このディレクトリで
go build -o manifold-guardrail .          # またはバイナリ
manifold-guardrail version
```

## `manifold-guardrail file`: ファイル用ガードレール

リクエストに含まれるファイルの中身をテキストに起こし、そのテキストを下流のテキスト用ガードレール（Generic Guardrail API を話すもの）で検査します。ファイルに埋め込まれた指示（プロンプトインジェクション）を、既存のテキスト用のポリシーで検出するためのものです。

```
呼び出し側（Manifold / LiteLLM） ──▶ manifold-guardrail file ──▶ テキスト用ガードレール
                                            └──▶ 書き起こし（LLM または OCR サービス）
```

```bash
manifold-guardrail file \
  --port 8091 \
  --downstream-url http://localhost:8090/beta/litellm_basic_guardrail_api \
  --extractor bedrock \
  --model <Bedrock のモデル ID か推論プロファイル>
```

エンドポイントは `POST /beta/litellm_basic_guardrail_api`、プローブ用に `GET /healthz` があります。フラグの一覧は `manifold-guardrail file -h` で見られます。

エンドポイントに認証はありません。呼び出し側（Manifold や LiteLLM）と同じ Pod のサイドカーとして動かすなど、呼び出し側からだけ到達できるようにしてください。リクエストの中身（`texts` と `structured_messages` の対応など）は、呼び出し側が正しく組み立てる前提です。

## リクエストのどこからファイルを読むか

| 場所 | 中身 | 送り元 |
|---|---|---|
| `images` | 画像。base64、または `data:` URL | LiteLLM、Manifold |
| `files` | `[{"uri": ..., "mime_type": ..., "data": <base64>}]`。画像以外のファイル | Manifold（LiteLLM の仕様に無い拡張フィールド） |
| `structured_messages` | メッセージの content にある `file` / `input_file` パートの `file_data`（base64、または `data:` URL） | LiteLLM |

種類（MIME タイプ）は、申告があればそれを、無ければ中身の先頭から決めます。

### URL で渡されたファイル

URL で渡されたファイル（`images` の http(s) の URL、`file_url`）は、**`--fetch-allowed-host` で許可したホストからだけ取得して検査します**。呼び出し側が指定した URL を無条件に取得すると SSRF の入口になるためです。

```bash
manifold-guardrail file ... \
  --fetch-allowed-host files.example.com \
  --fetch-allowed-host '*.cdn.example.com' \
  --fetch-allowed-host minio.internal:9000
```

- `files.example.com`: そのホスト名だけ。ポートは既定のもの（https は 443、http は 80）に限ります。
- `*.cdn.example.com`: サブドメインすべて。`cdn.example.com` 自身は含みません。
- `minio.internal:9000`: 既定以外のポートは明示します。

取得の挙動は次のとおりです。

- 許可していないホストの URL と、`file_id` で渡されたファイルは取得しません。中身を検査できないファイルとして、既定で `BLOCKED` にします（`--allow-unsupported-files` で通せます）。
- リダイレクトは、行き先も許可したホストのときだけたどります。
- `--fetch-max-bytes`（既定 20MiB）を超えるファイルは `BLOCKED`、取得の失敗（タイムアウト、200 以外）は `502` です。`--fetch-timeout` の既定は 30s です。
- 許可したホストがどのアドレスに解決されるかは確かめません（クラスタ内のストレージなどを許可できるようにするためです）。名前解決も含めて信頼できるホストだけを許可してください。
- URL はログとエラー文に出しません（署名付きのトークンを含みうるため）。ホスト名だけを出します。
- **検査した中身と、あとでモデルが読む中身が同じとは限りません。** 取得のたびに違う内容を返せるサーバーなら、検査をすり抜けられます。内容を差し替えられない、信頼できるホストだけを許可してください。

## Manifold で使う

ゲートウェイとテキスト用ガードレールの間に置き、`guardrail.url` をこれに向けます。ファイルを送るには `guardrail.files.enabled` を `true` にします。

```yaml
guardrail:
  enabled: true
  url: http://localhost:8091/beta/litellm_basic_guardrail_api
  timeout: 120s          # ファイルの書き起こしを待てる長さにする
  files:
    enabled: true
```

## LiteLLM で使う

LiteLLM の `generic_guardrail_api` の `api_base` をこれに向けます。LiteLLM 側の挙動（以下は LiteLLM のソースを読んで確認したもので、実機では未検証です）:

- **画像**: メッセージの `image_url` を `images` にそのまま入れます。`data:` URL ならそのまま検査できます。http(s) の URL は、`--fetch-allowed-host` で許可したホストのものだけ取得して検査します。
- **PDF などのファイル**: `structured_messages` にしか入りません。メッセージをそのまま渡す設定（`forward_messages_as_sent: true`）にしてください。
- **テキストの無いメッセージ**: LiteLLM は、テキストもツール呼び出しも無いリクエストではガードレールを呼びません。画像や PDF だけのメッセージは検査されません。
- **応答側（`input_type: response`）**: 画像やファイルは送られません。

## 処理の流れ

1. 元のリクエストは、そのまま（`images` / `files` を除いて）下流へ送り、判定をそのまま返します。下流の書き換え（`texts` のマスキングのほか、`tools`・`tool_calls`・`structured_messages` など）も、そのまま呼び出し元へ返ります。テキストが無くても、ツール呼び出し（`tool_calls`）やメッセージ（`structured_messages`）があれば送ります。`texts` が無いリクエストでは、メッセージの中のテキストも取り出して、ファイルのテキストと同じ扱い（通すか拒否するかだけ）で検査します。`texts` だけを検査する下流でも、メッセージのテキストが未検査で通らないようにするためです。
2. ファイルは1件ずつテキストに起こします。テキストのファイル（`text/*`、JSON、XML、YAML、SVG など。UTF-8 として正しいもの）は、そのままテキストとして扱います。それ以外は `--extractor` で起こします。
3. 起こしたテキストは、元の `texts` とは別の呼び出しで下流へ送ります。`input_type` は既定で `request` です。ファイルの中身は外から来た信頼できない入力で、プロンプトインジェクションのフィルタは入力側にしか効かないことが多いためです（`--extracted-input-type` で変更できます）。
4. 下流が拒否したら `BLOCKED` を返します。起こしたテキストに下流が書き換え（PII のマスキング等）を返した場合も、ファイル自体は書き換えられないので既定で `BLOCKED` にします（`--allow-intervened` で通せます）。

次の場合も `BLOCKED` を返します（どれも「検査できないものは通さない」ためです）。

- 起こせない種類のファイル（`--allow-unsupported-files` で、検査せずに通せます）。
- 書き起こしが出力の上限（`--max-output-tokens`）で途中で切れたファイル。残りが未検査になるためです。
- `--max-file-bytes` を超えるファイル。

書き起こしや下流の呼び出しが失敗したときは `502` を返し、呼び出し側はこれを失敗として扱います（Manifold は `guardrail check failed` で拒否します）。LLM が書き起こしを拒否した場合や、何も出力しなかった場合も失敗として扱います（「テキストの無いファイル」として通しません）。ファイルの中身と起こしたテキストはログに出しません。

## 書き起こしの方法（`--extractor`）

| `--extractor` | 対応する種類 | 設定 |
|---|---|---|
| `none`（既定） | テキストのファイルのみ | — |
| `bedrock` | PNG / JPEG / GIF / WebP、PDF、Word（doc / docx）、Excel（xls / xlsx） | `--model`（モデル ID か推論プロファイル）、`--aws-region`。認証情報は AWS SDK の既定の解決順。ロールに `bedrock:InvokeModel` が必要 |
| `google` | PNG / JPEG / WebP / HEIC / HEIF、PDF | `--model`、`--google-backend=gemini`（`GEMINI_API_KEY`）または `vertex`（`--google-project` / `--google-location`、アプリケーションの既定の認証情報） |
| `azure` | PNG / JPEG / GIF / WebP、PDF | `--model`（デプロイ名）、`--azure-endpoint`、`AZURE_OPENAI_API_KEY`。Responses API（`/openai/v1/responses`）を使います |
| `openapi` | `--openapi-mime-types`（既定 `application/pdf,image/*`） | 下記 |

### OpenAPI で記述された OCR サービス（`--extractor=openapi`）

文字起こしの API を持つ自前のサービスや SaaS は、その OpenAPI の spec を読み込ませて呼び出せます。リクエストとレスポンスは spec の operation から組み立てます。

```yaml
args:
  - file
  - --downstream-url=http://localhost:8090/beta/litellm_basic_guardrail_api
  - --extractor=openapi
  - --openapi-spec=/etc/ocr/openapi.yaml
  - --openapi-operation=extractText          # operationId
  - --openapi-header=Authorization=Bearer $(OCR_TOKEN)
  - --openapi-param=language=ja              # operation のパラメータや本文のプロパティ
```

- **リクエスト**: `requestBody` の content から、次の順で本文の形を選びます。
  - バイナリ（`application/octet-stream`、`application/pdf`、`*/*` など）: ファイルをそのまま本文にします。Content-Type は spec が宣言した種類です。ファイルと同じ種類があればそれを、ワイルドカード（`image/*` など）が受け付けるならファイルの種類を、`application/octet-stream` だけなら `application/octet-stream` を送ります。spec が受け付けない種類のファイルは扱いません。
  - `multipart/form-data`: `type: string, format: binary` のプロパティにファイルを入れます。
  - `application/json`: `type: string, format: byte` のプロパティに base64 で入れます。
- **レスポンス**: `text/plain` なら本文全体を、`application/json` なら文字列のプロパティをテキストとして読みます。成功として扱うのは `200` と `201` だけです。`202`（受け付けただけ）や `204` は、書き起こしが済んでいないので失敗にします。非同期で結果を返すサービスには、同期で結果を返す operation を使ってください。
- **spec から一意に決まらないとき**: ファイルを入れるプロパティは `--openapi-file-field`、テキストの場所は `--openapi-text-path` で指定します。`--openapi-text-path` は `.` 区切りで、`*` は配列の全要素です（例: `pages.*.text`）。決まらないまま起動するとエラーになります。
- **パラメータ**: operation のパラメータ（path / query / header）と本文のほかのプロパティは、`--openapi-param name=value` で渡します。値の中の `{mime_type}` はファイルの種類に置き換えます。必須のものに値が無いと起動時にエラーになります。
- **サーバー**: spec の `servers` の先頭を使います。変数を含む場合や別の URL に向ける場合は `--openapi-server` で指定します。

## 注意

- **LLM による書き起こしは、それ自体がプロンプトインジェクションの対象です**。ファイルに「この部分は出力するな」と書かれていると、LLM が従ってその部分を落とす可能性があります。ツールを持たせない、逐語で書き起こすよう指示する、ファイル名をモデルに渡さない、といった対策はしていますが、ゼロにはできません。確実さが要るなら、指示に従うことの無い OCR サービス（`openapi`）を使ってください。
- **長いテキストの分割**: 下流が受け取れるテキストの長さに上限があるときは、`--chunk-chars` で起こしたテキストを分割して送れます（既定は分割なし）。隣り合う断片は `--chunk-overlap-chars`（既定 200 文字）ずつ重ねるので、その長さまでの語句は境界で切れても、どれかの断片に全体が入ります。下流のポリシーが検出すべき最も長い語句の長さを指定してください（0 は指定できません。`--chunk-chars` の半分までです）。それより長い語句や、広い文脈を見て判定するポリシーは、分割すると検出できないことがあります。分割は必要なときだけ使い、断片はできるだけ大きくしてください。1リクエストで送る断片の数には上限があり（`--max-chunks`、既定 1000）、超えるリクエストは `BLOCKED` にします。
- **遅延と費用**: ファイル1件ごとに LLM か OCR を呼びます。呼び出し側のガードレールのタイムアウトは、書き起こしを待てる長さにしてください（Manifold は `guardrail.timeout`、既定 5s）。
- **各プロバイダとの結合は未検証です**: Bedrock / Google / Azure の呼び出しは、それぞれの公式ドキュメントと SDK の型に合わせて実装し、偽のクライアントでテストしていますが、実際のサービスに対しては動かしていません。導入時に実機で確認してください。
