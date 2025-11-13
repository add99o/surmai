import { Box, Title } from '@mantine/core';
import { useQuery } from 'react-query';
import { useParams } from 'react-router-dom';
import { apiClient } from '../../lib/api/client';

export function AIIterary() {
  const { tripId } = useParams();
  const { data, isLoading } = useQuery(['ai-itinerary', tripId], () =>
    apiClient.send(`/api/surmai/trip/${tripId}/ai-itinerary`, {})
  );

  const itinerary = data?.choices?.[0]?.message?.content;

  return (
    <Box>
      <Title order={1}>AI Itinerary</Title>
      {isLoading && <p>Loading...</p>}
      {itinerary && <pre>{JSON.stringify(JSON.parse(itinerary), null, 2)}</pre>}
    </Box>
  );
}
