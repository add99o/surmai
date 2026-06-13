import { pb, pbAdmin } from './pocketbase.ts';

import type { AssistantMessage, AssistantProposal, AssistantProposalDecision } from '../../../types/assistant.ts';

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

export const listTripAssistantProposals = (tripId: string): Promise<AssistantProposal[]> => {
  return pb
    .send(`/api/surmai/trip/${tripId}/assistant/proposals`, {
      method: 'GET',
    })
    .then((result) => result.proposals || []);
};

export const decideTripAssistantProposal = (
  tripId: string,
  proposalId: string,
  decision: AssistantProposalDecision
): Promise<{ status: string; message?: string; proposal?: AssistantProposal }> => {
  return pb.send(`/api/surmai/trip/${tripId}/assistant/proposals/${proposalId}/decision`, {
    method: 'POST',
    body: { decision },
  });
};

export const retryTripAssistantProposal = (
  tripId: string,
  proposalId: string
): Promise<{ status: string; message?: string; proposal?: AssistantProposal }> => {
  return pb.send(`/api/surmai/trip/${tripId}/assistant/proposals/${proposalId}/retry`, {
    method: 'POST',
    body: {},
  });
};
