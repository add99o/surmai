export type AssistantRole = 'user' | 'assistant';

export type AssistantSource = {
  title?: string;
  url: string;
};

export type AssistantProposalStatus = 'pending' | 'approved' | 'rejected' | 'expired' | 'applying' | 'failed';

export type AssistantProposalOperation = 'create' | 'update' | 'delete';

export type AssistantProposalEntityType = 'activity' | 'lodging' | 'transportation';

export type AssistantProposalChange = {
  operation: AssistantProposalOperation;
  entity_type: AssistantProposalEntityType;
  record_id?: string | null;
  fields: Record<string, unknown>;
  clear: string[];
  reason?: string | null;
  confidence: number;
  assumptions: string[];
  warnings: string[];
};

export type AssistantProposalDiff = {
  field: string;
  before?: unknown;
  after?: unknown;
};

export type AssistantProposalPreviewChange = {
  operation: AssistantProposalOperation;
  entity_type: AssistantProposalEntityType;
  record_id?: string;
  title: string;
  before?: Record<string, unknown>;
  after?: Record<string, unknown>;
  diff?: AssistantProposalDiff[];
  reason?: string;
  confidence?: number;
  assumptions?: string[];
  warnings?: string[];
};

export type AssistantProposalPreview = {
  title?: string;
  summary?: string;
  assumptions?: string[];
  warnings?: string[];
  changes?: AssistantProposalPreviewChange[];
};

export type AssistantProposal = {
  id: string;
  trip: string;
  user: string;
  status: AssistantProposalStatus;
  actionType: string;
  changes: AssistantProposalChange[];
  summary: string;
  preview?: AssistantProposalPreview;
  sources?: AssistantSource[];
  expiresAt: string;
  created: string;
  updated: string;
  error?: string;
  result?: Record<string, unknown>;
};

export type AssistantProposalDecision = 'approve' | 'reject' | 'decline' | 'timeout';

export type AssistantMessage = {
  id?: string;
  role: AssistantRole;
  content: string;
  metadata?: {
    sources?: AssistantSource[];
    [key: string]: unknown;
  };
  created?: string;
};

export type AssistantResponse = {
  message: AssistantMessage;
};

export type AssistantStreamEvent =
  | { type: 'message_created'; message: AssistantMessage }
  | { type: 'text_delta'; text: string }
  | { type: 'sources'; sources: AssistantSource[] }
  | { type: 'proposal_created'; proposal: AssistantProposal }
  | { type: 'done' }
  | { type: 'error'; message?: string };
