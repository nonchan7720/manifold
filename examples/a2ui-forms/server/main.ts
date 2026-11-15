// A2UI フォームのサンプルのバックエンド。
//
// ブラウザ（Web UI）と Claude Agent SDK の間に立ち、次を行う。
//
//   - Claude Agent SDK でエージェントを動かす。エージェントの MCP サーバーは Manifold。
//   - エージェントの出力に含まれる <a2ui> ブロック（A2UI Express）を A2UI Agent SDK で
//     コンパイルし、A2UI メッセージとしてブラウザへ流す。
//   - ブラウザから届いたフォームの送信（A2UI の action）をエージェントに渡す。
//   - <tool>__submit の呼び出しを、ユーザーが実際に送信した内容に限って通す。
//
// ブラウザは MCP を知らない。Manifold へ繋ぐのはエージェントだけ。

import {randomUUID} from 'node:crypto';
import {createServer, type IncomingMessage, type ServerResponse} from 'node:http';

import {A2uiError, A2uiGenerator, CatalogConfig} from '@a2ui/agent';
import {basicCatalog} from '@a2ui/web_core/v1_0';
import {type CanUseTool, query} from '@anthropic-ai/claude-agent-sdk';

const PORT = Number(process.env.PORT ?? 8787);
// Manifold の petstore エンドポイント（config.yaml 参照）。
const MANIFOLD_URL = process.env.MANIFOLD_URL ?? 'http://localhost:9999/mcp/petstore';
// Manifold は /mcp/<name> に Bearer トークンを要求する（既定では有無だけを確かめ、
// バックエンドの API へそのまま渡す）。Petstore は認証が要らないので、何でもよい。
// 実際のサービスでは、ログインしているユーザーのトークンを渡す。
const MANIFOLD_TOKEN = process.env.MANIFOLD_TOKEN ?? 'example';
const MCP_SERVER = 'petstore';
const TOOL_PREFIX = `mcp__${MCP_SERVER}__`;
// Manifold がフォームの送信用に並べるツールの接尾辞。
const SUBMIT_SUFFIX = '__submit';

// エージェントが <a2ui> ブロックを書けるよう、A2UI Agent SDK からシステムプロンプトの
// 断片をもらう。Manifold が返すブロックも同じ記法（A2UI Express）なので、エージェントは
// それをそのまま返答に含めればよい。
const generator = new A2uiGenerator([new CatalogConfig(basicCatalog)]);
const a2ui = generator.createProcessor({'1.0': {supportedCatalogIds: [basicCatalog.id]}});

const SYSTEM_PROMPT = `You are the assistant of a pet store. Use the ${MCP_SERVER} tools to look up and change the store's data.

When a tool result contains an <a2ui> block, include that block in your reply exactly as it is, and add at most one short sentence around it. Never retype or shorten it.

When the user message is an A2UI action (a JSON object with an "action" key), it means the user submitted a form: call the tool named by the action with the action's context as the arguments, then tell the user the outcome in one or two sentences.

${a2ui.promptSnippet}`;

/** ブラウザへ流すイベント。 */
type ServerEvent =
  | {type: 'text'; text: string}
  | {type: 'a2ui'; messages: unknown[]}
  | {type: 'tool'; name: string; input: unknown}
  | {type: 'denied'; name: string; reason: string}
  | {type: 'error'; message: string}
  | {type: 'done'; conversationId: string};

interface Conversation {
  /** Claude Agent SDK のセッション ID。2 ターン目以降の resume に使う。 */
  sessionId?: string;
  /** ユーザーが送信したフォーム。ツール名（<tool>__submit）→ 送信された値。 */
  submitted: Map<string, Record<string, unknown>>;
}

const conversations = new Map<string, Conversation>();

/**
 * ツール呼び出しの可否を決める。
 *
 * 読み取り系と、フォームを返すだけの書き込み系ツールの呼び出しは通す。実際に API を
 * 実行する <tool>__submit は、ユーザーが送信したフォームがあるときだけ通し、引数を
 * ユーザーが送信した値そのものに差し替える（モデルが書き換えた値では実行しない）。
 */
function canUseTool(conversation: Conversation, send: (e: ServerEvent) => void): CanUseTool {
  return async (toolName, input) => {
    if (!toolName.startsWith(TOOL_PREFIX)) {
      return {behavior: 'deny', message: `Only the ${MCP_SERVER} tools are available.`};
    }
    const name = toolName.slice(TOOL_PREFIX.length);
    if (!name.endsWith(SUBMIT_SUFFIX)) {
      return {behavior: 'allow', updatedInput: input};
    }
    const submitted = conversation.submitted.get(name);
    if (!submitted) {
      const reason = 'The user has not submitted this form.';
      send({type: 'denied', name, reason});
      return {
        behavior: 'deny',
        message: `${reason} Call ${name.slice(0, -SUBMIT_SUFFIX.length)} to show the form and wait for the user to submit it.`,
      };
    }
    conversation.submitted.delete(name);
    return {behavior: 'allow', updatedInput: submitted};
  };
}

/** エージェントのテキストを、地の文と A2UI メッセージに分けてブラウザへ流す。 */
function sendAssistantText(text: string, send: (e: ServerEvent) => void): void {
  try {
    for (const part of a2ui.parseResponse(text)) {
      if (part.type === 'text') {
        if (part.text.trim()) send({type: 'text', text: part.text.trim()});
      } else {
        send({type: 'a2ui', messages: part.a2ui});
      }
    }
  } catch (error) {
    // ブロックをコンパイルできなかった（エージェントが書き写しを誤った等）。
    const reason = error instanceof A2uiError ? error.message : String(error);
    send({type: 'error', message: `Could not read the A2UI block: ${reason}`});
    send({type: 'text', text});
  }
}

async function runTurn(
  conversationId: string,
  conversation: Conversation,
  prompt: string,
  send: (e: ServerEvent) => void,
): Promise<void> {
  const turn = query({
    prompt,
    options: {
      resume: conversation.sessionId,
      systemPrompt: SYSTEM_PROMPT,
      // 組み込みツール（Bash、ファイル操作など）は使わせない。
      tools: [],
      settingSources: [],
      mcpServers: {
        [MCP_SERVER]: {
          type: 'http',
          url: MANIFOLD_URL,
          headers: {Authorization: `Bearer ${MANIFOLD_TOKEN}`},
          alwaysLoad: true,
        },
      },
      canUseTool: canUseTool(conversation, send),
      maxTurns: 8,
      ...(process.env.MODEL ? {model: process.env.MODEL} : {}),
    },
  });
  for await (const message of turn) {
    if (message.type === 'system' && message.subtype === 'init') {
      conversation.sessionId = message.session_id;
    } else if (message.type === 'assistant') {
      for (const block of message.message.content) {
        if (block.type === 'text') {
          sendAssistantText(block.text, send);
        } else if (block.type === 'tool_use') {
          send({type: 'tool', name: block.name.replace(TOOL_PREFIX, ''), input: block.input});
        }
      }
    } else if (message.type === 'result' && message.subtype !== 'success') {
      send({type: 'error', message: `The agent stopped: ${message.subtype}`});
    }
  }
  send({type: 'done', conversationId});
}

async function readJson(req: IncomingMessage): Promise<Record<string, unknown>> {
  const chunks: Buffer[] = [];
  for await (const chunk of req) chunks.push(chunk as Buffer);
  const body = Buffer.concat(chunks).toString('utf8');
  return body ? (JSON.parse(body) as Record<string, unknown>) : {};
}

function conversationFor(id: unknown): {id: string; conversation: Conversation} {
  const key = typeof id === 'string' && conversations.has(id) ? id : randomUUID();
  let conversation = conversations.get(key);
  if (!conversation) {
    conversation = {submitted: new Map()};
    conversations.set(key, conversation);
  }
  return {id: key, conversation};
}

/** 1 ターン分のイベントを Server-Sent Events で返す。 */
async function streamTurn(
  res: ServerResponse,
  id: string,
  conversation: Conversation,
  prompt: string,
): Promise<void> {
  res.writeHead(200, {
    'Content-Type': 'text/event-stream',
    'Cache-Control': 'no-cache',
    Connection: 'keep-alive',
  });
  const send = (event: ServerEvent) => res.write(`data: ${JSON.stringify(event)}\n\n`);
  try {
    await runTurn(id, conversation, prompt, send);
  } catch (error) {
    send({type: 'error', message: String(error)});
    send({type: 'done', conversationId: id});
  }
  res.end();
}

const server = createServer(async (req, res) => {
  try {
    if (req.method === 'POST' && req.url === '/api/chat') {
      // ユーザーの発言。
      const body = await readJson(req);
      const {id, conversation} = conversationFor(body.conversationId);
      await streamTurn(res, id, conversation, String(body.message ?? ''));
      return;
    }
    if (req.method === 'POST' && req.url === '/api/action') {
      // フォームの送信（A2UI の action）。送信された値を控えてから、action をそのまま
      // エージェントへの入力にする。
      const body = await readJson(req);
      const {id, conversation} = conversationFor(body.conversationId);
      const action = body.action as {name?: unknown; context?: unknown} | undefined;
      if (typeof action?.name !== 'string') {
        res.writeHead(400).end('action.name is required');
        return;
      }
      if (action.name.endsWith(SUBMIT_SUFFIX)) {
        conversation.submitted.set(
          action.name,
          (action.context ?? {}) as Record<string, unknown>,
        );
      }
      await streamTurn(res, id, conversation, JSON.stringify({version: '1.0', action}));
      return;
    }
    res.writeHead(404).end();
  } catch (error) {
    console.error(error);
    if (!res.headersSent) res.writeHead(500);
    res.end(String(error));
  }
});

server.listen(PORT, () => {
  console.log(`a2ui-forms backend: http://localhost:${PORT} (Manifold: ${MANIFOLD_URL})`);
});
