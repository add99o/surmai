export type AssistantRole = 'user' | 'assistant';

export type AssistantMessage = {
  id?: string;
  role: AssistantRole;
  content: string;
};

export type AssistantResponse = {
  message: AssistantMessage;
};
