import { pb } from './pocketbase.ts';

import type { AssistantMessage, AssistantResponse } from '../../../types/assistant.ts';

export const askTripAssistant = (tripId: string, messages: AssistantMessage[]) => {
  return pb.send<AssistantResponse>(`/api/surmai/trip/${tripId}/assistant`, {
    method: 'POST',
    body: {
      messages,
    },
  });
};
