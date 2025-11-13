import { pb } from './pocketbase.ts';

import type { AiAssistantResponse, AiMessage } from '../../../types/ai.ts';

export const askItineraryAssistant = (
  tripId: string,
  messages: AiMessage[]
): Promise<AiAssistantResponse> => {
  return pb.send<AiAssistantResponse>(`/api/surmai/trip/${tripId}/ai/itinerary`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ messages }),
    signal: AbortSignal.timeout(60 * 1000),
  });
};
