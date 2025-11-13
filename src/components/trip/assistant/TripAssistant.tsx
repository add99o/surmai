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
  useMantineTheme,
} from '@mantine/core';
import { IconAlertCircle, IconSend } from '@tabler/icons-react';
import { useMutation } from '@tanstack/react-query';
import { nanoid } from 'nanoid';
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { useTranslation } from 'react-i18next';

import { askTripAssistant } from '../../../lib/api';
import { formatDate } from '../../../lib/time.ts';

import type { AssistantMessage, AssistantResponse } from '../../../types/assistant.ts';
import type { Trip } from '../../../types/trips.ts';
import type { ClientResponseError } from 'pocketbase';

type TripAssistantProps = {
  trip: Trip;
};

const MAX_PREVIEW_HEIGHT = 420;

export const TripAssistant = ({ trip }: TripAssistantProps) => {
  const theme = useMantineTheme();
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
                      ? theme.colorScheme === 'dark'
                        ? theme.colors.dark[6]
                        : theme.colors.gray[0]
                      : theme.fn.rgba(theme.primaryColor, 0.15),
                }}
              >
                <Text size="xs" fw={600} c="dimmed">
                  {message.role === 'assistant' ? t('assistant_label', 'Assistant') : t('you', 'You')}
                </Text>
                <Text size="sm" mt={4}>
                  {message.content}
                </Text>
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
