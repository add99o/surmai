export type AiRole = 'assistant' | 'user';

export type AiMessage = {
  role: AiRole;
  content: string;
};

export type AiAssistantResponse = {
  reply: string;
};
