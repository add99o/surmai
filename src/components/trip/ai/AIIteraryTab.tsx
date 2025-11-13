import { Box, Title, LoadingOverlay, Alert } from '@mantine/core';
import { useQuery } from '@tanstack/react-query';
import { useParams } from 'react-router-dom';
import { apiClient } from '../../../lib/api/client';
import { useTranslation } from 'react-i18next';

interface OpenAIResponse {
  choices: {
    message: {
      content: string;
    };
  }[];
}

export function AIIteraryTab() {
  const { tripId } = useParams();
  const { t } = useTranslation();
  const { data, isLoading, isError, error } = useQuery<OpenAIResponse>({
    queryKey: ['ai-itinerary', tripId],
    queryFn: () => apiClient.send(`/api/surmai/trip/${tripId}/ai-itinerary`, {}),
  });

  const itineraryContent = data?.choices?.[0]?.message?.content;

  let parsedItinerary = null;
  if (itineraryContent) {
    try {
      parsedItinerary = JSON.parse(itineraryContent);
    } catch (e) {
      // If parsing fails, we can just display the raw content
    }
  }

  return (
    <Box mt="md">
      <LoadingOverlay visible={isLoading} />
      {isError && (
        <Alert color="red" title={t('error', 'Error')}>
          {error.message}
        </Alert>
      )}
      {data && (
        <Box>
          <Title order={2}>{t('ai_generated_itinerary', 'AI Generated Itinerary')}</Title>
          <pre>{parsedItinerary ? JSON.stringify(parsedItinerary, null, 2) : itineraryContent}</pre>
        </Box>
      )}
    </Box>
  );
}
