import { pb, pbAdmin } from './pocketbase.ts';

import type { AssistantMessage } from '../../../types/assistant.ts';

export const sendTestPrompt = (prompt: string) => {
  return pbAdmin
    .send('/api/surmai/assistant/test-prompt', {
      method: 'POST',
      body: { prompt },
    })
    .then((result) => {
      return result.llmResponse;
    })
    .catch((err) => {
      return err.message || err.toString();
    });
};

export const testImapConnectivity = () => {
  return pbAdmin
    .send('/api/surmai/assistant/test-imap', {
      method: 'POST',
      body: {},
    })
    .then((result) => {
      return result.unreadEmailCount;
    });
};

export const triggerImportBookingsJob = () => {
  return pbAdmin.send('/api/surmai/assistant/import-bookings/trigger', {
    method: 'POST',
    body: {},
  });
};

export const listTripAssistantMessages = (tripId: string): Promise<AssistantMessage[]> => {
  return pb
    .send(`/api/surmai/trip/${tripId}/assistant/messages`, {
      method: 'GET',
    })
    .then((result) => result.messages || []);
};

export const createTripAssistantMessage = (
  tripId: string,
  message: Pick<AssistantMessage, 'content' | 'metadata'>
): Promise<AssistantMessage> => {
  return pb
    .send(`/api/surmai/trip/${tripId}/assistant/messages`, {
      method: 'POST',
      body: { ...message, role: 'user' },
    })
    .then((result) => result.message);
};

export const clearTripAssistantMessages = (tripId: string): Promise<{ deleted: number }> => {
  return pb.send(`/api/surmai/trip/${tripId}/assistant/messages`, {
    method: 'DELETE',
  });
};
