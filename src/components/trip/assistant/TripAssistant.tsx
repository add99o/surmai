import {
  Alert,
  Box,
  Button,
  Group,
  Loader,
  Paper,
  Stack,
  Text,
  Textarea,
  rem,
  useComputedColorScheme,
  useMantineTheme,
} from '@mantine/core';
import { IconAlertCircle, IconSend } from '@tabler/icons-react';
import { useMutation } from '@tanstack/react-query';
import { nanoid } from 'nanoid';
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { useTranslation } from 'react-i18next';
import DOMPurify from 'dompurify';

import { askTripAssistant } from '../../../lib/api';
import { formatDate } from '../../../lib/time.ts';
import classes from './TripAssistant.module.css';

import type { AssistantMessage, AssistantResponse } from '../../../types/assistant.ts';
import type { Trip } from '../../../types/trips.ts';
import type { ClientResponseError } from 'pocketbase';

type TripAssistantProps = {
  trip: Trip;
};

const MAX_PREVIEW_HEIGHT = 420;

export const TripAssistant = ({ trip }: TripAssistantProps) => {
  const theme = useMantineTheme();
  const colorScheme = useComputedColorScheme('light', { getInitialValueInEffect: true });
  const { t, i18n } = useTranslation();
  const [input, setInput] = useState('');
  const [error, setError] = useState<string | null>(null);
  const viewportRef = useRef<HTMLDivElement>(null);

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

  const mutation = useMutation({
    mutationFn: (conversation: AssistantMessage[]) => askTripAssistant(trip.id, conversation),
    onSuccess: (response: AssistantResponse) => {
      setMessages((prev) => [
        ...prev,
        {
          ...response.message,
          id: response.message.id ?? nanoid(),
        },
      ]);
      setError(null);
    },
    onError: (err: unknown) => {
      setError(resolveAssistantError(err, t('assistant_generic_error', 'Unable to reach the assistant. Please try again.')));
    },
  });

  const handleSend = () => {
    if (!input.trim() || mutation.isPending) {
      return;
    }
    const userMessage: AssistantMessage = {
      id: nanoid(),
      role: 'user',
      content: input.trim(),
    };
    const nextConversation = [...messages, userMessage];
    setMessages(nextConversation);
    setInput('');
    mutation.mutate(nextConversation);
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      handleSend();
    }
  };

  const conversationWithGreeting: AssistantMessage[] = [introMessage, ...messages];

  const renderedMessages = mutation.isPending
    ? [
        ...conversationWithGreeting,
        {
          id: 'assistant-typing',
          role: 'assistant',
          content: t('assistant_thinking', 'Thinking through the best answer...'),
        },
      ]
    : conversationWithGreeting;

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

      <Paper withBorder radius="md" p="sm">
        <Box
          ref={viewportRef}
          style={{
            maxHeight: rem(MAX_PREVIEW_HEIGHT),
            overflowY: 'auto',
            padding: `${rem(8)} ${rem(4)}`,
          }}
        >
          <Stack gap="sm">
            {renderedMessages.map((message) => (
              <Paper
                key={message.id}
                shadow="xs"
                radius="md"
                p="sm"
                style={{
                  alignSelf: message.role === 'assistant' ? 'flex-start' : 'flex-end',
                  maxWidth: '90%',
                  backgroundColor:
                    message.role === 'assistant'
                      ? colorScheme === 'dark'
                        ? 'var(--mantine-color-dark-6)'
                        : 'var(--mantine-color-gray-0)'
                      : `var(--mantine-color-${theme.primaryColor}-1, var(--mantine-primary-color-1))`,
                }}
              >
                <Text size="xs" fw={600} c="dimmed">
                  {message.role === 'assistant' ? t('assistant_label', 'Assistant') : t('you', 'You')}
                </Text>
                <Box
                  className={classes.messageBody}
                  dangerouslySetInnerHTML={{ __html: sanitizeMarkdown(message.content) }}
                />
              </Paper>
            ))}
            {mutation.isPending && (
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

      <Stack gap="xs">
        <Textarea
          placeholder={t('assistant_input_placeholder', 'Ask about flights, dinner plans, or request ideas...')}
          minRows={3}
          autosize
          value={input}
          onChange={(event) => setInput(event.currentTarget.value)}
          onKeyDown={handleKeyDown}
          disabled={mutation.isPending}
        />
        <Group justify="space-between">
          <Text size="xs" c="dimmed">
            {t('assistant_input_hint', 'Press Enter to send, Shift+Enter for a new line.')}
          </Text>
          <Button leftSection={<IconSend size={16} />} onClick={handleSend} disabled={!input.trim() || mutation.isPending}>
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

  const maybeResponse = error as ClientResponseError;
  if (maybeResponse?.response?.message) {
    return maybeResponse.response.message;
  }

  if (maybeResponse?.message) {
    return maybeResponse.message;
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
