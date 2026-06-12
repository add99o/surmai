export type AssistantRole = 'user' | 'assistant';

export type AssistantSource = {
  title?: string;
  url: string;
};

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
