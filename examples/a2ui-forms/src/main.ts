// A2UI フォームのサンプルの Web UI。
//
// チャットの発言をバックエンドへ送り、返ってきた地の文と A2UI メッセージを表示する。
// A2UI メッセージは公式のレンダラ（@a2ui/web_core の MessageProcessor と @a2ui/lit の
// <a2ui-surface>）で描画する。フォームの送信ボタンが発火した action は、そのまま
// バックエンドへ送る。この画面は MCP を呼ばない。

import '@a2ui/lit';
import type {LitComponentApi} from '@a2ui/lit';
import {renderMarkdown} from '@a2ui/markdown-it';
import {
  type ActionPayload,
  MessageProcessor,
  type SurfaceModel,
  basicCatalog,
  injectBasicCatalogStyles,
  setMarkdownRenderer,
} from '@a2ui/web_core/v1_0';

type ServerEvent =
  | {type: 'text'; text: string}
  | {type: 'a2ui'; messages: unknown[]}
  | {type: 'tool'; name: string; input: unknown}
  | {type: 'denied'; name: string; reason: string}
  | {type: 'error'; message: string}
  | {type: 'done'; conversationId: string};

injectBasicCatalogStyles();
setMarkdownRenderer(renderMarkdown);

const log = document.querySelector<HTMLElement>('#log')!;
const form = document.querySelector<HTMLFormElement>('#composer')!;
const input = document.querySelector<HTMLInputElement>('#message')!;
const sendButton = form.querySelector<HTMLButtonElement>('button')!;

let conversationId: string | undefined;
let busy = false;

function setBusy(value: boolean): void {
  busy = value;
  sendButton.disabled = value;
  document.body.dataset.busy = String(value);
}

function addEntry(kind: string, text?: string): HTMLElement {
  const entry = document.createElement('div');
  entry.className = `entry ${kind}`;
  if (text !== undefined) entry.textContent = text;
  log.append(entry);
  entry.scrollIntoView({block: 'end'});
  return entry;
}

// フォームの送信ボタンは A2UI の action を発火する。送信済みのフォームは操作できない
// ようにして、action をバックエンド経由でエージェントに渡す。
const processor = new MessageProcessor<LitComponentApi>([basicCatalog], (action: ActionPayload) => {
  if (busy) return;
  document
    .querySelector<HTMLElement>(`[data-surface-id="${CSS.escape(action.surfaceId)}"]`)
    ?.setAttribute('inert', '');
  addEntry('user', `(submitted the form: ${action.name})`);
  void turn('/api/action', {action});
});

// surface が作られたら、その場所にレンダラの要素を置く。
processor.onSurfaceCreated((surface: SurfaceModel<LitComponentApi>) => {
  const entry = addEntry('surface');
  entry.dataset.surfaceId = surface.id;
  const element = document.createElement('a2ui-surface') as HTMLElement & {
    surface?: SurfaceModel<LitComponentApi>;
  };
  element.surface = surface;
  entry.append(element);
});

function handleEvent(event: ServerEvent): void {
  switch (event.type) {
    case 'text':
      addEntry('assistant', event.text);
      break;
    case 'a2ui':
      processor.processMessages(event.messages as Parameters<typeof processor.processMessages>[0]);
      break;
    case 'tool':
      addEntry('tool', `tool: ${event.name} ${JSON.stringify(event.input)}`);
      break;
    case 'denied':
      addEntry('tool', `blocked: ${event.name} (${event.reason})`);
      break;
    case 'error':
      addEntry('error', event.message);
      break;
    case 'done':
      conversationId = event.conversationId;
      break;
  }
}

/** 1 ターン分のリクエストを送り、Server-Sent Events で返るイベントを順に処理する。 */
async function turn(path: string, body: Record<string, unknown>): Promise<void> {
  setBusy(true);
  try {
    const response = await fetch(path, {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({conversationId, ...body}),
    });
    if (!response.ok || !response.body) {
      addEntry('error', `request failed: ${response.status}`);
      return;
    }
    const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
    let buffer = '';
    for (;;) {
      const {value, done} = await reader.read();
      if (done) break;
      buffer += value;
      let end: number;
      while ((end = buffer.indexOf('\n\n')) >= 0) {
        const line = buffer.slice(0, end);
        buffer = buffer.slice(end + 2);
        if (line.startsWith('data: ')) handleEvent(JSON.parse(line.slice(6)) as ServerEvent);
      }
    }
  } catch (error) {
    addEntry('error', String(error));
  } finally {
    setBusy(false);
  }
}

form.addEventListener('submit', event => {
  event.preventDefault();
  const message = input.value.trim();
  if (!message || busy) return;
  input.value = '';
  addEntry('user', message);
  void turn('/api/chat', {message});
});
