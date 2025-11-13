import { Box, Title, Code } from '@mantine/core';
import { useQuery } from '@tanstack/react-query';
import { useParams } from 'react-router-dom';
import { apiClient } from '../../lib/api/client';

interface OpenAIResponse {
  choices: {
    items: {
      message?: {
        content?: string;
      };
    }[];
  }[];
}

export function AIIterary() {
  const { tripId } = useParams();
  const { data, isLoading, error } = useQuery<OpenAIResponse>({
    queryKey: ['ai-itinerary', tripId],
    queryFn: () => apiClient.send(`/api/surmai/trip/${tripId}/ai-itinerary`, {}),
  });

  const itineraryContent = data?.choices?.[0]?.items?.[0]?.message?.content;

  if (isLoading) {
    return <p>Loading...</p>;
  }

  if (error) {
    return <Code block>Error fetching itinerary: {(error as Error).message}</Code>;
  }

  return (
    <Box>
      <Title order={1}>AI Itinerary</Title>
      {itineraryContent ? (
        <pre>{itineraryContent}</pre>
      ) : (
        <p>No itinerary content found in the AI response.</p>
      )}
    </Box>
  );
}
