import { Agent, RunState, isOpenAIResponsesRawModelStreamEvent, run, tool, webSearchTool } from '@openai/agents';
import { z } from 'zod';
import process from 'node:process';

type RunnerInput = {
  mode: 'run' | 'resume';
  model: string;
  messages?: Array<{ role: 'user' | 'assistant'; content: string }>;
  tripContext?: unknown;
  sdkState?: string;
  decision?: 'approve' | 'reject';
  rejectionMessage?: string;
};

type Source = {
  title?: string;
  url: string;
};

const nullableString = z.string().nullable();
const nullableNumber = z.number().nullable();
const fieldValue = z.union([z.string(), z.number(), z.boolean(), z.null(), z.record(z.string(), z.unknown())]);

const proposalFieldSet = z.object({
  name: nullableString,
  description: nullableString,
  address: nullableString,
  type: nullableString,
  origin: nullableString,
  destination: nullableString,
  provider: nullableString,
  start_time: nullableString,
  end_time: nullableString,
  departure_time: nullableString,
  arrival_time: nullableString,
  confirmation: nullableString,
  notes: nullableString,
  link: nullableString,
  cost_value: nullableNumber,
  cost_currency: nullableString,
  metadata: z.record(z.string(), fieldValue).nullable(),
});

const itineraryChange = z.object({
  operation: z.enum(['create', 'update', 'delete']),
  entity_type: z.enum(['activity', 'lodging', 'transportation']),
  record_id: nullableString,
  fields: proposalFieldSet,
  clear: z.array(z.string()),
  reason: nullableString,
  confidence: z.number().min(0).max(1),
  assumptions: z.array(z.string()),
  warnings: z.array(z.string()),
});

const proposalSchema = z.object({
  title: z.string(),
  summary: z.string(),
  changes: z.array(itineraryChange).min(1),
  assumptions: z.array(z.string()),
  warnings: z.array(z.string()),
});

const proposeItineraryChanges = tool({
  name: 'propose_itinerary_changes',
  description:
    'Prepare one reviewable itinerary proposal. Use this for any requested create, update, delete, or batch change. The app applies changes only after human approval.',
  parameters: proposalSchema,
  strict: true,
  needsApproval: true,
  async execute() {
    return 'The approved itinerary proposal was applied by Surmai.';
  },
});

function createAgent(model: string) {
  return new Agent({
    name: 'Surmai Trip Assistant',
    model,
    instructions: [
      'You are Surmai’s trip co-planner.',
      'Answer from trip context first. Use web search only for current external facts such as hours, closures, weather, availability, or disruptions.',
      'For itinerary changes, call propose_itinerary_changes and do not claim changes were applied.',
      'Ask one concise clarification question if required fields are genuinely missing.',
      'Keep answers concise. Use 12-hour times for people and RFC3339 values in tool fields.',
      'Avoid destructive changes unless explicitly requested.',
    ].join('\n'),
    tools: [webSearchTool({ searchContextSize: 'medium' }), proposeItineraryChanges],
    modelSettings: {
      store: false,
      reasoning: { effort: 'low' },
      text: { verbosity: 'low' },
      toolChoice: 'auto',
    },
  });
}

function emit(payload: Record<string, unknown>) {
  process.stdout.write(`${JSON.stringify(payload)}\n`);
}

function extractSources(value: unknown, seen: Set<string>, out: Source[]) {
  if (!value || typeof value !== 'object') {
    return;
  }

  if (Array.isArray(value)) {
    value.forEach((entry) => extractSources(entry, seen, out));
    return;
  }

  const record = value as Record<string, unknown>;
  const url = stringValue(record.url) || stringValue(record.uri) || stringValue(record.link);
  if (url?.startsWith('http') && !seen.has(url)) {
    seen.add(url);
    const title = stringValue(record.title) || stringValue(record.name) || stringValue(record.text);
    out.push(title ? { title, url } : { url });
  }

  Object.values(record).forEach((entry) => extractSources(entry, seen, out));
}

function stringValue(value: unknown) {
  return typeof value === 'string' && value.trim() ? value.trim() : '';
}

function normalizeMessages(input: RunnerInput): string {
  const context = JSON.stringify(input.tripContext ?? {}, null, 2);
  const history = (input.messages ?? [])
    .filter((message) => message.content?.trim())
    .map((message) => `${message.role === 'assistant' ? 'Assistant' : 'User'}: ${message.content}`)
    .join('\n\n');

  return [`Latest trip context JSON:\n${context}`, history ? `Conversation history:\n${history}` : ''].filter(Boolean).join('\n\n');
}

async function runStream(input: RunnerInput) {
  const agent = createAgent(input.model);
  let stream;

  if (input.mode === 'resume') {
    if (!input.sdkState) {
      throw new Error('sdkState is required for resume mode');
    }
    const state = await RunState.fromString(agent, input.sdkState);
    const interruptions = state.getInterruptions();
    for (const interruption of interruptions) {
      if (input.decision === 'approve') {
        state.approve(interruption);
      } else {
        state.reject(interruption, {
          message: input.rejectionMessage || 'The traveler rejected this itinerary proposal.',
        });
      }
    }
    stream = await run(agent, state, { stream: true, maxTurns: 8 });
  } else {
    stream = await run(agent, normalizeMessages(input), { stream: true, maxTurns: 8 });
  }

  const seenSources = new Set<string>();
  const allSources: Source[] = [];

  for await (const event of stream) {
    if (isOpenAIResponsesRawModelStreamEvent(event)) {
      const rawType = event.data.event.type;
      if (rawType === 'response.output_text.delta') {
        const delta = stringValue((event.data.event as { delta?: unknown }).delta);
        if (delta) {
          emit({ type: 'text_delta', text: delta });
        }
      }

      const sources: Source[] = [];
      extractSources(event.data.event, seenSources, sources);
      if (sources.length > 0) {
        allSources.push(...sources);
        emit({ type: 'sources', sources });
      }
    }
  }

  await stream.completed;

  if (stream.interruptions?.length) {
    const first = stream.interruptions[0] as { arguments?: unknown };
    emit({
      type: 'proposal_interruption',
      arguments: first.arguments,
      interruptions: stream.interruptions.map((interruption) => ({
        name: interruption.name,
        arguments: interruption.arguments,
      })),
      sdkState: stream.state.toString(),
      sources: allSources,
    });
    return;
  }

  emit({
    type: 'done',
    finalOutput: stream.finalOutput ?? '',
    sdkState: stream.state.toString(),
    sources: allSources,
  });
}

async function main() {
  const chunks: Buffer[] = [];
  for await (const chunk of process.stdin) {
    chunks.push(Buffer.from(chunk));
  }
  const input = JSON.parse(Buffer.concat(chunks).toString('utf8')) as RunnerInput;
  await runStream(input);
}

main().catch((error: unknown) => {
  emit({ type: 'error', message: error instanceof Error ? error.message : String(error) });
  process.exitCode = 1;
});
