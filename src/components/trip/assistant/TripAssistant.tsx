import {
  ActionIcon,
  Alert,
  Badge,
  Box,
  Button,
  Group,
  Loader,
  Paper,
  ScrollArea,
  Stack,
  Text,
  Textarea,
  Timeline,
  Tooltip,
} from '@mantine/core';
import {
  IconAlertCircle,
  IconCheck,
  IconExternalLink,
  IconRefresh,
  IconSend,
  IconTrash,
  IconX,
} from '@tabler/icons-react';
import { nanoid } from 'nanoid';
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { useQueryClient } from '@tanstack/react-query';
import DOMPurify from 'dompurify';
import dayjs from 'dayjs';

import {
  clearTripAssistantMessages,
  decideTripAssistantProposal,
  listTripAssistantMessages,
  listTripAssistantProposals,
  retryTripAssistantProposal,
} from '../../../lib/api';
import { pb } from '../../../lib/api/pocketbase/pocketbase.ts';
import { formatDate } from '../../../lib/time.ts';
import classes from './TripAssistant.module.css';

import type {
  AssistantMessage,
  AssistantProposal,
  AssistantProposalDecision,
  AssistantProposalPreviewChange,
  AssistantSource,
  AssistantStreamEvent,
} from '../../../types/assistant.ts';
import type { Trip } from '../../../types/trips.ts';

type TripAssistantProps = {
  trip: Trip;
};

type TimelineEntry =
  | {
      type: 'message';
      key: string;
      sequence: number;
      timestamp: number;
      message: AssistantMessage;
    }
  | {
      type: 'proposal';
      key: string;
      sequence: number;
      timestamp: number;
      proposal: AssistantProposal;
    };

export const TripAssistant = ({ trip }: TripAssistantProps) => {
  const { t, i18n } = useTranslation();
  const queryClient = useQueryClient();
  const [input, setInput] = useState('');
  const [messages, setMessages] = useState<AssistantMessage[]>([]);
  const [proposals, setProposals] = useState<AssistantProposal[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [isStreaming, setIsStreaming] = useState(false);
  const [isLoading, setIsLoading] = useState(false);
  const [applyingProposalId, setApplyingProposalId] = useState<string | null>(null);
  const viewportRef = useRef<HTMLDivElement>(null);
  const controllerRef = useRef<AbortController | null>(null);

  const introMessage = useMemo<AssistantMessage>(
    () => ({
      id: 'assistant-intro',
      role: 'assistant',
      content: t('assistant_intro', 'Hi! I am your Surmai AI guide for {{tripName}}. Ask me about your plans, timing, or itinerary changes.', {
        tripName: trip.name,
      }),
    }),
    [t, trip.name]
  );

  useEffect(() => {
    let ignore = false;
    setError(null);
    setInput('');
    setIsLoading(true);
    Promise.all([listTripAssistantMessages(trip.id), listTripAssistantProposals(trip.id)])
      .then(([storedMessages, storedProposals]) => {
        if (!ignore) {
          setMessages(storedMessages);
          setProposals(storedProposals);
        }
      })
      .catch((err) => {
        if (!ignore) {
          setError(resolveAssistantError(err, t('assistant_history_load_failed', 'Unable to load assistant history.')));
        }
      })
      .finally(() => {
        if (!ignore) {
          setIsLoading(false);
        }
      });
    return () => {
      ignore = true;
    };
  }, [t, trip.id]);

  useEffect(() => {
    viewportRef.current?.scrollTo({ top: viewportRef.current.scrollHeight });
  }, [messages, proposals, introMessage]);

  useEffect(() => {
    return () => controllerRef.current?.abort();
  }, []);

  const activeProposal = proposals.find((proposal) => proposal.status === 'pending' || proposal.status === 'applying');
  const isApplying = !!applyingProposalId;

  const refreshProposals = async () => {
    setProposals(await listTripAssistantProposals(trip.id));
  };

  const invalidateItinerary = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['buildActivitiesIndex', trip.id] }),
      queryClient.invalidateQueries({ queryKey: ['buildTransportationIndex', trip.id] }),
      queryClient.invalidateQueries({ queryKey: ['buildLodgingIndex', trip.id] }),
      queryClient.invalidateQueries({ queryKey: ['listActivities', trip.id] }),
      queryClient.invalidateQueries({ queryKey: ['listTransportations', trip.id] }),
      queryClient.invalidateQueries({ queryKey: ['listLodgings', trip.id] }),
      queryClient.invalidateQueries({ queryKey: ['trip', trip.id] }),
    ]);
  };

  const handleSend = async () => {
    const content = input.trim();
    if (!content || isStreaming || activeProposal || isApplying) {
      if (activeProposal) {
        setError(t('assistant_pending_warning', 'Please approve or reject the pending change first.'));
      }
      return;
    }

    const assistantId = nanoid();
    setMessages((prev) => [
      ...prev,
      {
        id: assistantId,
        role: 'assistant',
        content: '',
        created: new Date().toISOString(),
      },
    ]);
    setInput('');
    setError(null);

    try {
      await streamAssistantReply(content, assistantId);
      await refreshProposals();
    } catch (err) {
      setError(resolveAssistantError(err, t('assistant_generic_error', 'Unable to reach the assistant. Please try again.')));
      setMessages((prev) =>
        prev.map((message) =>
          message.id === assistantId ? { ...message, content: t('assistant_error_short', 'Something went wrong.') } : message
        )
      );
    }
  };

  const streamAssistantReply = async (content: string, assistantId: string) => {
    setIsStreaming(true);
    const controller = new AbortController();
    controllerRef.current = controller;

    try {
      const response = await fetch(`/api/surmai/trip/${trip.id}/assistant/stream`, {
        method: 'POST',
        headers: buildAuthHeaders(),
        body: JSON.stringify({ messages: [{ role: 'user', content }] }),
        signal: controller.signal,
      });

      if (!response.ok || !response.body) {
        throw new Error((await response.text()) || 'Assistant stream failed.');
      }

      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';

      while (true) {
        const { value, done } = await reader.read();
        if (done) {
          break;
        }
        buffer += decoder.decode(value, { stream: true });
        const parsed = parseSSEPayloads(buffer);
        buffer = parsed.remaining;
        for (const event of parsed.events) {
          handleStreamEvent(event, assistantId);
          if (event.type === 'done') {
            return;
          }
        }
      }
    } finally {
      controllerRef.current = null;
      setIsStreaming(false);
    }
  };

  const handleStreamEvent = (event: AssistantStreamEvent, assistantId: string) => {
    if (event.type === 'message_created') {
      setMessages((prev) => {
        const withoutDuplicate = prev.filter((message) => message.id !== event.message.id);
        const assistantIndex = withoutDuplicate.findIndex((message) => message.id === assistantId);
        if (assistantIndex === -1) {
          return [...withoutDuplicate, event.message];
        }
        return [
          ...withoutDuplicate.slice(0, assistantIndex),
          event.message,
          ...withoutDuplicate.slice(assistantIndex),
        ];
      });
      return;
    }
    if (event.type === 'text_delta') {
      appendAssistantText(assistantId, event.text);
      return;
    }
    if (event.type === 'sources') {
      appendAssistantSources(assistantId, event.sources);
      return;
    }
    if (event.type === 'proposal_created') {
      setProposals((prev) => [...prev.filter((proposal) => proposal.id !== event.proposal.id), event.proposal]);
      setMessages((prev) =>
        prev.map((message) =>
          message.id === assistantId ? { ...message, content: event.proposal.summary || t('assistant_pending_change', 'Pending change') } : message
        )
      );
      return;
    }
    if (event.type === 'error') {
      throw new Error(event.message || 'Assistant stream failed.');
    }
  };

  const appendAssistantText = (assistantId: string, chunk: string) => {
    setMessages((prev) =>
      prev.map((message) => (message.id === assistantId ? { ...message, content: message.content + chunk } : message))
    );
  };

  const appendAssistantSources = (assistantId: string, sources: AssistantSource[]) => {
    setMessages((prev) =>
      prev.map((message) => {
        if (message.id !== assistantId) {
          return message;
        }
        const existing = message.metadata?.sources || [];
        const nextSources = [...existing];
        sources.forEach((source) => {
          if (source.url && !nextSources.some((item) => item.url === source.url)) {
            nextSources.push(source);
          }
        });
        return { ...message, metadata: { ...message.metadata, sources: nextSources } };
      })
    );
  };

  const handleProposalDecision = async (proposal: AssistantProposal, decision: AssistantProposalDecision) => {
    setApplyingProposalId(proposal.id);
    setError(null);
    try {
      const payload =
        decision === 'approve' || decision === 'reject' || decision === 'decline' || decision === 'timeout'
          ? await decideTripAssistantProposal(trip.id, proposal.id, decision)
          : undefined;
      if (payload?.proposal) {
        setProposals((prev) => prev.map((item) => (item.id === proposal.id ? payload.proposal! : item)));
      }
      if (payload?.message) {
        setMessages((prev) => [...prev, { id: nanoid(), role: 'assistant', content: payload.message!, created: new Date().toISOString() }]);
      }
      if (decision === 'approve') {
        await invalidateItinerary();
      }
      await refreshProposals();
    } catch (err) {
      setError(resolveAssistantError(err, t('assistant_generic_error', 'Unable to reach the assistant. Please try again.')));
      await refreshProposals().catch(() => undefined);
    } finally {
      setApplyingProposalId(null);
    }
  };

  const handleProposalRetry = async (proposal: AssistantProposal) => {
    setApplyingProposalId(proposal.id);
    setError(null);
    try {
      const payload = await retryTripAssistantProposal(trip.id, proposal.id);
      if (payload.proposal) {
        setProposals((prev) => prev.map((item) => (item.id === proposal.id ? payload.proposal! : item)));
      }
      if (payload.message) {
        setMessages((prev) => [...prev, { id: nanoid(), role: 'assistant', content: payload.message!, created: new Date().toISOString() }]);
      }
      await invalidateItinerary();
      await refreshProposals();
    } catch (err) {
      setError(resolveAssistantError(err, t('assistant_generic_error', 'Unable to reach the assistant. Please try again.')));
      await refreshProposals().catch(() => undefined);
    } finally {
      setApplyingProposalId(null);
    }
  };

  const handleClearChat = async () => {
    if (isStreaming || isApplying) {
      return;
    }
    setError(null);
    try {
      await clearTripAssistantMessages(trip.id);
      setMessages([]);
      await refreshProposals();
    } catch (err) {
      setError(resolveAssistantError(err, t('assistant_clear_failed', 'Unable to clear assistant history.')));
    }
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      void handleSend();
    }
  };

  const timelineMessages = [introMessage, ...messages].filter((message) => message.content.trim());
  const visibleProposals = proposals.filter((proposal) => proposal.status !== 'expired' || dayjs(proposal.updated).isAfter(dayjs().subtract(1, 'day')));
  const timelineEntries = buildTimelineEntries(timelineMessages, visibleProposals);

  return (
    <Stack gap="md" mt="md" className={classes.assistantWorkspace}>
      <Paper withBorder className={classes.contextHeader}>
        <Group justify="space-between" align="flex-start" gap="md">
          <Stack gap={3}>
            <Text fw={700} className={classes.tripTitle}>
              {trip.name}
            </Text>
            <Text size="sm" c="dimmed">
              {t('assistant_trip_summary', 'Planning window: {{start}} to {{end}}', {
                start: formatDate(i18n.language, trip.startDate),
                end: formatDate(i18n.language, trip.endDate),
              })}
            </Text>
          </Stack>
          <Tooltip label={t('assistant_clear_chat', 'Clear chat')}>
            <ActionIcon
              variant="subtle"
              color="red"
              onClick={handleClearChat}
              disabled={isStreaming || isLoading || isApplying || messages.length === 0}
              aria-label={t('assistant_clear_chat', 'Clear chat')}
            >
              <IconTrash size={17} />
            </ActionIcon>
          </Tooltip>
        </Group>
      </Paper>

      {error && (
        <Alert
          icon={<IconAlertCircle size={16} />}
          color="red"
          variant="light"
          title={t('assistant_error', 'Assistant error')}
          onClose={() => setError(null)}
          withCloseButton
        >
          {error}
        </Alert>
      )}

      <Paper withBorder={false} p={0} className={classes.timelineShell}>
        <ScrollArea viewportRef={viewportRef} className={classes.timelineViewport}>
          <Timeline active={timelineEntries.length} bulletSize={22} lineWidth={1}>
            {isLoading && (
              <Timeline.Item bullet={<Loader size={12} />}>
                <Text size="sm" c="dimmed">
                  {t('assistant_history_loading', 'Loading assistant history...')}
                </Text>
              </Timeline.Item>
            )}
            {timelineEntries.map((entry) => (
              <Timeline.Item
                key={entry.key}
                bullet={entry.type === 'message' ? (entry.message.role === 'assistant' ? 'A' : 'Y') : <ProposalBullet proposal={entry.proposal} />}
              >
                {entry.type === 'message' ? (
                  renderMessage(entry.message, t)
                ) : (
                  <ProposalCard
                    proposal={entry.proposal}
                    applying={applyingProposalId === entry.proposal.id}
                    onApprove={() => handleProposalDecision(entry.proposal, 'approve')}
                    onReject={() => handleProposalDecision(entry.proposal, 'reject')}
                    onRetry={() => handleProposalRetry(entry.proposal)}
                  />
                )}
              </Timeline.Item>
            ))}
            {isStreaming && (
              <Timeline.Item bullet={<Loader size={12} />}>
                <Text size="sm" c="dimmed">
                  {t('assistant_typing_indicator', 'Generating reply...')}
                </Text>
              </Timeline.Item>
            )}
          </Timeline>
        </ScrollArea>
      </Paper>

      <Paper withBorder className={classes.composer}>
        <Textarea
          classNames={{ input: classes.inputArea }}
          placeholder={t('assistant_input_placeholder', 'Ask about timing, plans, or request an itinerary change...')}
          minRows={2}
          autosize
          variant="unstyled"
          value={input}
          onChange={(event) => setInput(event.currentTarget.value)}
          onKeyDown={handleKeyDown}
          disabled={isStreaming || isApplying}
        />
        <Group justify="space-between" align="center" className={classes.composerFooter}>
          <Text size="xs" c="dimmed">
            {activeProposal
              ? t('assistant_pending_warning_short', 'Review the pending proposal before sending another request.')
              : t('assistant_input_hint', 'Enter sends. Shift+Enter adds a new line.')}
          </Text>
          <Button
            size="sm"
            leftSection={<IconSend size={16} />}
            onClick={handleSend}
            disabled={!input.trim() || isStreaming || !!activeProposal || isApplying}
          >
            {t('assistant_send', 'Send')}
          </Button>
        </Group>
      </Paper>
    </Stack>
  );
};

const ProposalBullet = ({ proposal }: { proposal: AssistantProposal }) => {
  if (proposal.status === 'approved') {
    return <IconCheck size={13} />;
  }
  if (proposal.status === 'rejected' || proposal.status === 'expired') {
    return <IconX size={13} />;
  }
  if (proposal.status === 'failed') {
    return <IconAlertCircle size={13} />;
  }
  return <IconRefresh size={13} />;
};

const ProposalCard = ({
  proposal,
  applying,
  onApprove,
  onReject,
  onRetry,
}: {
  proposal: AssistantProposal;
  applying: boolean;
  onApprove: () => void;
  onReject: () => void;
  onRetry: () => void;
}) => {
  const statusColor = proposal.status === 'failed' ? 'red' : proposal.status === 'approved' ? 'green' : proposal.status === 'pending' ? 'blue' : 'gray';
  const preview = proposal.preview;
  const previewChanges = preview?.changes || [];
  const isPending = proposal.status === 'pending';

  return (
    <Paper withBorder className={classes.proposalCard}>
      <Stack gap="sm">
        <Group justify="space-between" align="flex-start" className={classes.proposalHeader}>
          <Stack gap={5} className={classes.proposalSummary}>
            <Group gap="xs">
              <Badge color={proposal.actionType === 'delete' ? 'red' : proposal.actionType === 'update' ? 'yellow' : 'green'} variant="light">
                {proposal.actionType === 'batch' ? 'Batch' : labelize(proposal.actionType)}
              </Badge>
              <Badge color={statusColor} variant="dot">
                {labelize(proposal.status)}
              </Badge>
            </Group>
            <Text fw={700} className={classes.proposalTitle}>
              {preview?.title || proposal.summary}
            </Text>
            {preview?.summary && (
              <Text size="sm" c="dimmed" className={classes.proposalDescription}>
                {preview.summary}
              </Text>
            )}
          </Stack>
          {isPending && (
            <Text size="xs" c="dimmed" className={classes.proposalExpiry}>
              Expires {dayjs(proposal.expiresAt).format('MMM D, h:mm A')}
            </Text>
          )}
        </Group>

        <Stack gap="xs">
          {previewChanges.map((change, index) => (
            <PreviewChange key={`${proposal.id}-${index}`} change={change} />
          ))}
        </Stack>

        {renderWarnings([...(preview?.assumptions || []), ...(preview?.warnings || [])])}
        {proposal.sources && proposal.sources.length > 0 && renderSources(proposal.sources)}
        {proposal.error && (
          <Alert color="red" variant="light" icon={<IconAlertCircle size={14} />}>
            {proposal.error}
          </Alert>
        )}

        <Group justify="flex-end" className={classes.proposalActions}>
          {isPending && (
            <>
              <Button variant="light" color="gray" onClick={onReject} disabled={applying}>
                Reject
              </Button>
              <Button onClick={onApprove} loading={applying}>
                Approve
              </Button>
            </>
          )}
          {proposal.status === 'failed' && (
            <Button leftSection={<IconRefresh size={16} />} onClick={onRetry} loading={applying}>
              Retry
            </Button>
          )}
        </Group>
      </Stack>
    </Paper>
  );
};

const PreviewChange = ({ change }: { change: AssistantProposalPreviewChange }) => {
  const previewEntries = previewFieldEntries(change.operation === 'delete' ? change.before : change.after);
  const diffEntries = previewDiffEntries(change.diff);

  return (
    <Box className={classes.previewChange}>
      <Stack gap={6}>
        <Group gap="xs" align="flex-start">
          <Badge size="sm" variant="light">
            {labelize(change.operation)}
          </Badge>
          <Badge size="sm" variant="outline">
            {labelize(change.entity_type)}
          </Badge>
          <Text fw={600} size="sm" className={classes.previewTitle}>
            {change.title}
          </Text>
        </Group>
        {change.operation === 'update' && diffEntries.length > 0 && (
          <Stack gap={4}>
            {diffEntries.map((diff) => (
              <Group key={diff.field} gap="xs" align="flex-start" className={classes.diffRow}>
                <Text size="xs" fw={700} c="dimmed">
                  {labelize(diff.field)}
                </Text>
                <Text size="xs" className={classes.diffValue}>
                  {formatValue(diff.before)} {'->'} {formatValue(diff.after)}
                </Text>
              </Group>
            ))}
          </Stack>
        )}
        {change.operation !== 'update' && previewEntries.length > 0 && (
          <Stack gap={4} className={classes.previewFields}>
            {previewEntries.map((entry) => (
              <Group key={entry.field} gap="xs" align="flex-start" className={classes.previewFieldRow}>
                <Text size="xs" fw={700} c="dimmed">
                  {labelize(entry.field)}
                </Text>
                <Text size="sm" c={change.operation === 'delete' ? 'red' : undefined} className={classes.previewValue}>
                  {entry.value}
                </Text>
              </Group>
            ))}
          </Stack>
        )}
      </Stack>
    </Box>
  );
};

const renderMessage = (message: AssistantMessage, t: ReturnType<typeof useTranslation>['t']) => (
  <Paper
    className={`${classes.chatBubble} ${
      message.role === 'assistant' ? classes.assistantBubble : classes.userBubble
    }`}
  >
    <Text size="xs" fw={700} className={classes.messageMeta}>
      {message.role === 'assistant' ? t('assistant_label', 'Assistant') : t('you', 'You')}
    </Text>
    <Box className={classes.messageBody} dangerouslySetInnerHTML={{ __html: sanitizeMarkdown(message.content) }} />
    {message.metadata?.sources && message.metadata.sources.length > 0 && renderSources(message.metadata.sources)}
  </Paper>
);

const renderSources = (sources: AssistantSource[]) => (
  <Group gap={6} mt="xs">
    {sources.map((source, index) => (
      <a key={`${source.url}-${index}`} href={source.url} target="_blank" rel="noreferrer" className={classes.sourceChip}>
        <span className={classes.sourceIndex}>{index + 1}</span>
        <span className={classes.sourceLabel}>{sourceLabel(source)}</span>
        <IconExternalLink size={12} />
      </a>
    ))}
  </Group>
);

const renderWarnings = (items: string[]) => {
  const visible = items.filter(Boolean);
  if (visible.length === 0) {
    return null;
  }
  return (
    <Stack gap={3}>
      {visible.map((item) => (
        <Text key={item} size="xs" c="dimmed">
          {item}
        </Text>
      ))}
    </Stack>
  );
};

const resolveAssistantError = (error: unknown, fallback: string) => {
  if (typeof error === 'string') {
    return error;
  }
  if (error instanceof Error && error.message) {
    return error.message;
  }
  return fallback;
};

const sanitizeMarkdown = (text: string) => DOMPurify.sanitize(markdownToHtml(text));

const markdownToHtml = (raw: string) => {
  if (!raw.trim()) {
    return '';
  }
  return raw
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .split(/\n{2,}/)
    .map((block) => `<p>${block.replace(/\n/g, '<br />').replace(/\*\*(.*?)\*\*/g, '<strong>$1</strong>').replace(/`([^`]+)`/g, '<code>$1</code>')}</p>`)
    .join('');
};

const parseSSEPayloads = (buffer: string): { events: AssistantStreamEvent[]; remaining: string } => {
  const segments = buffer.split('\n\n');
  const remaining = segments.pop() ?? '';
  const events: AssistantStreamEvent[] = [];
  segments.forEach((segment) => {
    const line = segment
      .split('\n')
      .map((value) => value.trim())
      .find((value) => value.startsWith('data:'));
    if (!line) {
      return;
    }
    try {
      events.push(JSON.parse(line.slice(5).trim()) as AssistantStreamEvent);
    } catch {
      // Ignore partial or malformed stream chunks.
    }
  });
  return { events, remaining };
};

const buildAuthHeaders = (): HeadersInit => {
  const headers: HeadersInit = { 'Content-Type': 'application/json' };
  if (pb.authStore.token) {
    headers.Authorization = `Bearer ${pb.authStore.token}`;
  }
  return headers;
};

const sourceLabel = (source: AssistantSource) => {
  if (source.title) {
    return source.title;
  }
  try {
    return new URL(source.url).hostname.replace(/^www\./, '');
  } catch {
    return source.url;
  }
};

const labelize = (value: string) => value.replace(/_/g, ' ').replace(/\b\w/g, (match) => match.toUpperCase());

const formatValue = (value: unknown) => {
  if (value === null || value === undefined || value === '') {
    return 'None';
  }
  if (typeof value === 'object') {
    return JSON.stringify(value);
  }
  return String(value);
};

const previewFieldEntries = (value?: Record<string, unknown>) => {
  if (!value) {
    return [];
  }
  return Object.entries(value).reduce<Array<{ field: string; value: string }>>((entries, [key, entry]) => {
    if (key === 'id' || key === 'metadata' || key === 'cost_currency' || entry === null || entry === undefined || entry === '') {
      return entries;
    }
    if (key === 'cost_value') {
      const currency = typeof value.cost_currency === 'string' && value.cost_currency ? ` ${value.cost_currency}` : '';
      entries.push({ field: 'cost', value: `${formatValue(entry)}${currency}` });
      return entries;
    }
    entries.push({ field: key, value: formatValue(entry) });
    return entries;
  }, []);
};

const buildTimelineEntries = (messages: AssistantMessage[], proposals: AssistantProposal[]) => {
  const entries: TimelineEntry[] = messages.map((message, index) => ({
    type: 'message',
    key: `message-${message.id || index}`,
    sequence: index,
    timestamp: timelineTimestamp(message.created, index),
    message,
  }));

  proposals.forEach((proposal, index) => {
    const entry: TimelineEntry = {
      type: 'proposal',
      key: `proposal-${proposal.id}`,
      sequence: messages.length + index,
      timestamp: timelineTimestamp(proposal.created || proposal.updated, messages.length + index),
      proposal,
    };
    const insertAt = entries.findIndex((item) => item.type === 'message' && item.timestamp > entry.timestamp);
    if (insertAt === -1) {
      entries.push(entry);
    } else {
      entries.splice(insertAt, 0, entry);
    }
  });

  return entries;
};

const timelineTimestamp = (value: string | undefined, fallback: number) => {
  if (!value) {
    return fallback;
  }
  const parsed = dayjs(value).valueOf();
  return Number.isFinite(parsed) ? parsed : fallback;
};

const previewDiffEntries = (diffs: AssistantProposalPreviewChange['diff'] = []) => {
  const visible = diffs.filter((diff) => diff.field !== 'metadata');
  if (visible.length > 0) {
    return visible;
  }
  return diffs.length > 0 ? [{ field: 'details', before: 'Previous place details', after: 'Updated place details' }] : [];
};
