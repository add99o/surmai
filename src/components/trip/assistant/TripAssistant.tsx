import { Alert, Box, Button, Group, Loader, Paper, Stack, Text, Textarea, rem } from '@mantine/core';
import { IconAlertCircle, IconSend } from '@tabler/icons-react';
import { nanoid } from 'nanoid';
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { useTranslation } from 'react-i18next';
import DOMPurify from 'dompurify';
import dayjs from 'dayjs';

import { pb } from '../../../lib/api/pocketbase/pocketbase.ts';
import { formatDate } from '../../../lib/time.ts';
import classes from './TripAssistant.module.css';

import type { AssistantMessage } from '../../../types/assistant.ts';
import type { Trip } from '../../../types/trips.ts';

type TripAssistantProps = {
  trip: Trip;
};

type AssistantProposal = {
  id: string;
  tool: string;
  arguments: Record<string, any>;
  summary: string;
  expiresAt: string;
};

type ProposalDecision = 'approve' | 'decline' | 'timeout';

const MAX_PREVIEW_HEIGHT = 420;

export const TripAssistant = ({ trip }: TripAssistantProps) => {
  const { t, i18n } = useTranslation();
  const [input, setInput] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [isStreaming, setIsStreaming] = useState(false);
  const [pendingProposal, setPendingProposal] = useState<AssistantProposal | null>(null);
  const [proposalCountdown, setProposalCountdown] = useState<number>(0);
  const viewportRef = useRef<HTMLDivElement>(null);
  const controllerRef = useRef<AbortController | null>(null);

  const introMessage = useMemo<AssistantMessage>(
    () => ({
      id: 'assistant-intro',
      role: 'assistant',
      content: t('assistant_intro', 'Hi! I am your Surmai AI guide for {{tripName}}. Ask me about your plans, timing, or get suggestions.', {
        tripName: trip.name,
      }),
    }),
    [t, trip.name]
  );

  const [messages, setMessages] = useState<AssistantMessage[]>([]);

  useEffect(() => {
    setMessages([]);
    setError(null);
    setInput('');
  }, [introMessage, trip.id]);

  useEffect(() => {
    const el = viewportRef.current;
    if (el) {
      el.scrollTop = el.scrollHeight;
    }
  }, [messages, introMessage]);

  useEffect(() => {
    return () => {
      controllerRef.current?.abort();
    };
  }, []);

  useEffect(() => {
    if (!pendingProposal) {
      setProposalCountdown(0);
      return;
    }

    let timeoutHandled = false;
    const expires = dayjs(pendingProposal.expiresAt);

    const updateCountdown = () => {
      const diff = expires.diff(dayjs(), 'second');
      if (diff <= 0) {
        setProposalCountdown(0);
        if (!timeoutHandled) {
          timeoutHandled = true;
          void handleProposalDecision('timeout');
        }
        return;
      }
      setProposalCountdown(diff);
    };

    updateCountdown();
    const interval = window.setInterval(updateCountdown, 1000);
    return () => window.clearInterval(interval);
  }, [pendingProposal]);

  const handleSend = async () => {
    if (!input.trim() || isStreaming) {
      return;
    }
    if (pendingProposal) {
      setError(t('assistant_pending_warning', 'Please approve or decline the pending change first.'));
      return;
    }

    const userMessage: AssistantMessage = {
      id: nanoid(),
      role: 'user',
      content: input.trim(),
    };

    const assistantId = nanoid();
    const assistantPlaceholder: AssistantMessage = {
      id: assistantId,
      role: 'assistant',
      content: '',
    };

    const nextConversation = [...messages, userMessage];
    setMessages((prev) => [...prev, userMessage, assistantPlaceholder]);
    setInput('');
    setError(null);

    try {
      await streamAssistantReply(nextConversation, assistantId);
    } catch (err) {
      const fallback = t('assistant_generic_error', 'Unable to reach the assistant. Please try again.');
      setError(resolveAssistantError(err, fallback));
      setMessages((prev) =>
        prev.map((message) =>
          message.id === assistantId ? { ...message, content: t('assistant_error_short', 'Something went wrong.') } : message
        )
      );
    }
  };

  const streamAssistantReply = async (conversation: AssistantMessage[], assistantId: string) => {
    setIsStreaming(true);
    const controller = new AbortController();
    controllerRef.current = controller;

    try {
      const response = await fetch(`/api/surmai/trip/${trip.id}/assistant/stream`, {
        method: 'POST',
        headers: buildAuthHeaders(),
        body: JSON.stringify({ messages: conversation }),
        signal: controller.signal,
      });

      if (!response.ok || !response.body) {
        const message = await response.text();
        throw new Error(message || 'Assistant stream failed.');
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
        const { events, remaining } = parseSSEPayloads(buffer);
        buffer = remaining;
        for (const event of events) {
          if (event.type === 'proposal' && event.proposal) {
            const proposal = event.proposal as AssistantProposal;
            setPendingProposal(proposal);
            setMessages((prev) =>
              prev.map((message) =>
                message.role === 'assistant' && message.content === ''
                  ? { ...message, content: proposal.summary }
                  : message
              )
            );
            await reader.cancel().catch(() => undefined);
            return;
          }
          if (event.type === 'delta' && event.text) {
            appendAssistantText(assistantId, event.text);
          } else if (event.type === 'error') {
            throw new Error(event.message || 'Assistant stream failed.');
          } else if (event.type === 'done') {
            return;
          }
        }
      }

      if (buffer.trim()) {
        const { events } = parseSSEPayloads(buffer + '\n\n');
        for (const event of events) {
          if (event.type === 'delta' && event.text) {
            appendAssistantText(assistantId, event.text);
          }
        }
      }
    } finally {
      controllerRef.current = null;
      setIsStreaming(false);
    }
  };

  const appendAssistantText = (assistantId: string, chunk: string) => {
    setMessages((prev) =>
      prev.map((message) => {
        if (message.id === assistantId) {
          return {
            ...message,
            content: message.content + chunk,
          };
        }
        return message;
      })
    );
  };

  const handleProposalDecision = async (decision: ProposalDecision) => {
    if (!pendingProposal) {
      return;
    }
    setIsStreaming(true);
    try {
      const response = await fetch(
        `/api/surmai/trip/${trip.id}/assistant/proposals/${pendingProposal.id}/decision`,
        {
          method: 'POST',
          headers: buildAuthHeaders(),
          body: JSON.stringify({ decision }),
        }
      );
      const payload = await response.json();

      if (!response.ok) {
        throw new Error(payload?.error || 'Unable to process the decision.');
      }

      if (payload?.message) {
        setMessages((prev) => [
          ...prev,
          {
            id: nanoid(),
            role: 'assistant',
            content: payload.message as string,
          },
        ]);
      }
    } catch (err) {
      const fallback = t('assistant_generic_error', 'Unable to reach the assistant. Please try again.');
      setError(resolveAssistantError(err, fallback));
    } finally {
      setPendingProposal(null);
      setIsStreaming(false);
    }
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      handleSend();
    }
  };

  const conversationWithGreeting: AssistantMessage[] = [introMessage, ...messages];

  return (
    <Stack gap="md" mt="md">
      <Stack gap={4}>
        <Text fw={600}>{trip.name}</Text>
        <Text size="sm" c="dimmed">
          {t('assistant_trip_summary', 'Planning window: {{start}} -> {{end}}', {
            start: formatDate(i18n.language, trip.startDate),
            end: formatDate(i18n.language, trip.endDate),
          })}
        </Text>
        <Text size="sm" c="dimmed">
          {t(
            'assistant_context_notice',
            'The assistant sees your latest itinerary, destinations, expenses, and notes to keep answers grounded.'
          )}
        </Text>
      </Stack>

      {error && (
        <Alert icon={<IconAlertCircle size={16} />} color="red" variant="light" title={t('assistant_error', 'Assistant error')} onClose={() => setError(null)} withCloseButton>
          {error}
        </Alert>
      )}

      <Paper withBorder={false} radius="lg" p={0} className={classes.chatScroller}>
        <Box ref={viewportRef} style={{ maxHeight: rem(MAX_PREVIEW_HEIGHT), overflowY: 'auto', padding: rem(16) }}>
          <Stack gap="sm">
            {conversationWithGreeting.map((message) => (
              <Paper
                key={message.id}
                className={`${classes.chatBubble} ${
                  message.role === 'assistant' ? classes.assistantBubble : classes.userBubble
                }`}
                style={{ alignSelf: message.role === 'assistant' ? 'flex-start' : 'flex-end', maxWidth: '92%' }}
              >
                <Text
                  size="xs"
                  fw={700}
                  className={`${classes.messageMeta} ${
                    message.role === 'assistant' ? classes.messageMetaAssistant : classes.messageMetaUser
                  }`}
                >
                  {message.role === 'assistant' ? t('assistant_label', 'Assistant') : t('you', 'You')}
                </Text>
                <Box
                  className={classes.messageBody}
                  dangerouslySetInnerHTML={{ __html: sanitizeMarkdown(message.content) }}
                />
              </Paper>
            ))}
            {isStreaming && (
              <Group gap="xs">
                <Loader size="sm" />
                <Text size="sm" c="dimmed">
                  {t('assistant_typing_indicator', 'Generating reply...')}
                </Text>
              </Group>
            )}
          </Stack>
        </Box>
      </Paper>

      {pendingProposal && (
        <Paper withBorder radius="lg" className={classes.proposalCard}>
          <Stack gap="sm">
            <Group justify="space-between" align="flex-start">
              <div>
                <Text fw={600}>{t('assistant_pending_change', 'Pending change')}</Text>
                <Text size="sm" c="dimmed">
                  {pendingProposal.summary}
                </Text>
              </div>
              <Text size="sm" className={classes.proposalCountdown}>
                {proposalCountdown > 0
                  ? t('proposal_expires_in', { defaultValue: '{{count}}s left', count: proposalCountdown })
                  : t('proposal_expired', 'Expired')}
              </Text>
            </Group>
            {renderProposalDetails(pendingProposal)}
            <Group justify="flex-end" className={classes.proposalActions}>
              <Button
                variant="light"
                onClick={() => handleProposalDecision('decline')}
                disabled={isStreaming}
              >
                {t('assistant_decline', 'Decline')}
              </Button>
              <Button onClick={() => handleProposalDecision('approve')} disabled={isStreaming}>
                {t('assistant_approve', 'Approve')}
              </Button>
            </Group>
          </Stack>
        </Paper>
      )}

      <Stack gap="xs">
        <Textarea
          classNames={{ input: classes.inputArea }}
          placeholder={t('assistant_input_placeholder', 'Ask about flights, dinner plans, or request ideas...')}
          minRows={3}
          autosize
          value={input}
          onChange={(event) => setInput(event.currentTarget.value)}
          onKeyDown={handleKeyDown}
          disabled={isStreaming}
        />
        <Group justify="space-between">
          <Text size="xs" c="dimmed">
            {t('assistant_input_hint', 'Press Enter to send, Shift+Enter for a new line.')}
          </Text>
          <Button leftSection={<IconSend size={16} />} onClick={handleSend} disabled={!input.trim() || isStreaming}>
            {t('assistant_send', 'Send')}
          </Button>
        </Group>
      </Stack>
    </Stack>
  );
};

const resolveAssistantError = (error: unknown, fallback: string) => {
  if (!error) {
    return fallback;
  }

  if (typeof error === 'string') {
    return error;
  }

  if (error instanceof Error && error.message) {
    return error.message;
  }

  return fallback;
};

const sanitizeMarkdown = (text: string) => {
  const html = markdownToHtml(text);
  return DOMPurify.sanitize(html);
};

const markdownToHtml = (raw: string) => {
  if (!raw || !raw.trim()) {
    return '';
  }

  const normalized = raw.replace(/\r\n/g, '\n');
  const lines = normalized.split('\n');
  const htmlParts: string[] = [];
  let listType: 'ul' | 'ol' | null = null;
  let inCodeBlock = false;
  let codeLanguage = '';
  let codeLines: string[] = [];

  const closeList = () => {
    if (listType) {
      htmlParts.push(`</${listType}>`);
      listType = null;
    }
  };

  const closeCodeBlock = () => {
    if (!inCodeBlock) {
      return;
    }
    const langAttr = codeLanguage ? ` class="language-${codeLanguage}"` : '';
    htmlParts.push(`<pre><code${langAttr}>${escapeHtml(codeLines.join('\n'))}</code></pre>`);
    inCodeBlock = false;
    codeLanguage = '';
    codeLines = [];
  };

  for (const line of lines) {
    const trimmedLine = line.trim();

    if (trimmedLine.startsWith('```')) {
      if (inCodeBlock) {
        closeCodeBlock();
      } else {
        closeList();
        inCodeBlock = true;
        codeLanguage = trimmedLine.slice(3).trim();
      }
      continue;
    }

    if (inCodeBlock) {
      codeLines.push(line);
      continue;
    }

    if (trimmedLine === '') {
      closeList();
      htmlParts.push('<br />');
      continue;
    }

    const headingMatch = trimmedLine.match(/^(#{1,6})\s+(.*)$/);
    if (headingMatch) {
      closeList();
      const level = headingMatch[1].length;
      htmlParts.push(`<h${level}>${applyInlineFormatting(headingMatch[2])}</h${level}>`);
      continue;
    }

    if (/^[-*+]\s+/.test(trimmedLine)) {
      if (listType !== 'ul') {
        closeList();
        listType = 'ul';
        htmlParts.push('<ul>');
      }
      const itemText = trimmedLine.replace(/^[-*+]\s+/, '');
      htmlParts.push(`<li>${applyInlineFormatting(itemText)}</li>`);
      continue;
    }

    if (/^\d+\.\s+/.test(trimmedLine)) {
      if (listType !== 'ol') {
        closeList();
        listType = 'ol';
        htmlParts.push('<ol>');
      }
      const itemText = trimmedLine.replace(/^\d+\.\s+/, '');
      htmlParts.push(`<li>${applyInlineFormatting(itemText)}</li>`);
      continue;
    }

    if (/^>\s?/.test(trimmedLine)) {
      closeList();
      const quote = trimmedLine.replace(/^>\s?/, '');
      htmlParts.push(`<blockquote>${applyInlineFormatting(quote)}</blockquote>`);
      continue;
    }

    closeList();
    htmlParts.push(`<p>${applyInlineFormatting(trimmedLine)}</p>`);
  }

  closeCodeBlock();
  closeList();

  return htmlParts.join('');
};

const escapeHtml = (value: string) => {
  return value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
};

const applyInlineFormatting = (value: string) => {
  let output = escapeHtml(value);
  output = output.replace(/(\*\*|__)(.*?)\1/g, '<strong>$2</strong>');
  output = output.replace(/(\*|_)(.*?)\1/g, '<em>$2</em>');
  output = output.replace(/~~(.*?)~~/g, '<del>$1</del>');
  output = output.replace(/`([^`]+)`/g, '<code>$1</code>');
  output = output.replace(/\[([^\]]+)]\(([^)]+)\)/g, '<a href="$2" target="_blank" rel="noreferrer">$1</a>');
  return output;
};

const parseSSEPayloads = (buffer: string) => {
  const segments = buffer.split('\n\n');
  const remaining = segments.pop() ?? '';
  const events: Array<Record<string, any>> = [];

  segments.forEach((segment) => {
    const line = segment
      .split('\n')
      .map((l) => l.trim())
      .find((l) => l.startsWith('data:'));
    if (!line) {
      return;
    }
    const payload = line.slice(5).trim();
    if (!payload) {
      return;
    }
    try {
      events.push(JSON.parse(payload));
    } catch {
      // Ignore malformed chunks
    }
  });

  return { events, remaining };
};

const buildAuthHeaders = (): HeadersInit => {
  const headers: HeadersInit = {
    'Content-Type': 'application/json',
  };

  const token = pb.authStore.token;
  if (token) {
    headers.Authorization = `Bearer ${token}`;
  }

  return headers;
};

const renderProposalDetails = (proposal: AssistantProposal) => {
  const entries = Object.entries(proposal.arguments || {});
  if (entries.length === 0) {
    return null;
  }
  return (
    <Stack gap={4}>
      {entries.map(([key, value]) => (
        <Group key={key} gap="xs">
          <Text size="sm" fw={600} c="dimmed" style={{ textTransform: 'capitalize' }}>
            {key.replace(/_/g, ' ')}:
          </Text>
          <Text size="sm">
            {typeof value === 'object' && value !== null ? JSON.stringify(value) : String(value)}
          </Text>
        </Group>
      ))}
    </Stack>
  );
};
