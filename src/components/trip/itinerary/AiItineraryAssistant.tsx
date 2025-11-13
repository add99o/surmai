import {
  Alert,
  Badge,
  Button,
  Card,
  Group,
  Paper,
  ScrollArea,
  Stack,
  Text,
  Textarea,
} from '@mantine/core';
import { IconSparkles, IconWifiOff } from '@tabler/icons-react';
import { useCallback, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { useSurmaiContext } from '../../../app/useSurmaiContext.ts';
import { askItineraryAssistant } from '../../../lib/api';

import type { AiMessage } from '../../../types/ai.ts';

const initialAssistantMessage = (intro: string): AiMessage => ({
  role: 'assistant',
  content: intro,
});

export const AiItineraryAssistant = ({ tripId }: { tripId: string }) => {
  const { t } = useTranslation();
  const { offline } = useSurmaiContext();
  const [messages, setMessages] = useState<AiMessage[]>([
    initialAssistantMessage(
      t(
        'ai_itinerary_assistant_intro',
        "Let me know how you'd like to adapt this trip and I'll reference the latest itinerary to help."
      )
    ),
  ]);
  const [inputValue, setInputValue] = useState('');
  const [isThinking, setIsThinking] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const viewportRef = useRef<HTMLDivElement | null>(null);

  const isSendDisabled = useMemo(() => {
    return offline || isThinking || inputValue.trim() === '';
  }, [offline, isThinking, inputValue]);

  const scrollToLatestMessage = useCallback(() => {
    if (viewportRef.current) {
      viewportRef.current.scrollTop = viewportRef.current.scrollHeight;
    }
  }, []);

  const pushMessage = useCallback((nextMessage: AiMessage) => {
    setMessages((current) => {
      const updated = [...current, nextMessage];
      queueMicrotask(scrollToLatestMessage);
      return updated;
    });
  }, [scrollToLatestMessage]);

  const handleSend = useCallback(async () => {
    const trimmed = inputValue.trim();
    if (trimmed === '') {
      return;
    }
    setInputValue('');
    setError(null);

    const userMessage: AiMessage = { role: 'user', content: trimmed };
    setMessages((current) => [...current, userMessage]);
    setIsThinking(true);

    try {
      const response = await askItineraryAssistant(tripId, [...messages, userMessage]);
      pushMessage({ role: 'assistant', content: response.reply });
    } catch (err) {
      console.error(err);
      setError(
        t(
          'ai_itinerary_assistant_error',
          'We could not reach the AI itinerary assistant. Please try again in a moment.'
        )
      );
    } finally {
      setIsThinking(false);
    }
  }, [inputValue, messages, pushMessage, t, tripId]);

  return (
    <Card shadow="sm" radius="md" withBorder>
      <Group gap="xs" mb="xs">
        <IconSparkles size={18} color="var(--mantine-color-yellow-6)" />
        <Text fw={600}>{t('ai_itinerary_assistant_title', 'AI itinerary assistant')}</Text>
        <Badge size="xs" variant="light">
          {t('ai_itinerary_assistant_model', 'Powered by GPT-5-mini')}
        </Badge>
      </Group>
      <Text size="sm" c="dimmed" mb="sm">
        {t(
          'ai_itinerary_assistant_description',
          'Chat with Surmai AI for personalized adjustments that consider your most recent trip data.'
        )}
      </Text>

      {offline && (
        <Alert mb="sm" variant="light" color="yellow" icon={<IconWifiOff size={16} />}>
          {t('ai_itinerary_assistant_offline', 'Go online to use the AI itinerary assistant.')}
        </Alert>
      )}

      <ScrollArea h={220} viewportRef={viewportRef} type="auto" offsetScrollbars>
        <Stack gap="xs" pr="sm">
          {messages.map((message, idx) => (
            <Paper
              key={`ai-message-${idx}`}
              radius="md"
              p="sm"
              bg={message.role === 'assistant' ? 'var(--mantine-color-gray-1)' : 'var(--mantine-color-blue-light)'}
            >
              <Text size="xs" c="dimmed" mb={4}>
                {message.role === 'assistant'
                  ? t('ai_itinerary_assistant_role_ai', 'Surmai AI')
                  : t('ai_itinerary_assistant_role_user', 'You')}
              </Text>
              <Text size="sm">{message.content}</Text>
            </Paper>
          ))}
          {isThinking && (
            <Paper radius="md" p="sm" bg="var(--mantine-color-gray-1)">
              <Text size="xs" c="dimmed" mb={4}>
                {t('ai_itinerary_assistant_role_ai', 'Surmai AI')}
              </Text>
              <Text size="sm">
                {t('ai_itinerary_assistant_waiting', 'Thinking through the most recent plans…')}
              </Text>
            </Paper>
          )}
        </Stack>
      </ScrollArea>

      {error && (
        <Alert mt="sm" color="red" variant="light">
          {error}
        </Alert>
      )}

      <Textarea
        mt="sm"
        minRows={2}
        autosize
        placeholder={t(
          'ai_itinerary_assistant_placeholder',
          'Ask for dinner ideas near our day two hotel or help reorganizing airport transfers…'
        )}
        value={inputValue}
        onChange={(event) => setInputValue(event.currentTarget.value)}
        onKeyDown={(event) => {
          if (event.key === 'Enter' && !event.shiftKey) {
            event.preventDefault();
            if (!isSendDisabled) {
              void handleSend();
            }
          }
        }}
      />

      <Group justify="space-between" mt="sm">
        <Text size="xs" c="dimmed">
          {t('ai_itinerary_assistant_privacy', 'Only the current trip itinerary is shared with OpenAI for this chat.')}
        </Text>
        <Button onClick={() => void handleSend()} loading={isThinking} disabled={isSendDisabled}>
          {t('ai_itinerary_assistant_cta', 'Ask Surmai AI')}
        </Button>
      </Group>
    </Card>
  );
};
