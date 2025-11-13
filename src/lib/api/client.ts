import PocketBase from 'pocketbase';

export const apiClient = new PocketBase(import.meta.env.VITE_POCKETBASE_URL);
