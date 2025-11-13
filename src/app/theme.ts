import { createTheme, rem } from '@mantine/core';

// iOS System Colors
const iosBlue = [
  '#E3F2FD',
  '#BBDEFB',
  '#90CAF9',
  '#64B5F6',
  '#42A5F5',
  '#2196F3',
  '#007AFF', // iOS Blue primary
  '#1976D2',
  '#1565C0',
  '#0D47A1',
];

const iosGray = [
  '#F9F9F9',
  '#F2F2F7', // iOS system background light
  '#E5E5EA',
  '#D1D1D6',
  '#C7C7CC',
  '#AEAEB2',
  '#8E8E93',
  '#636366',
  '#48484A',
  '#1C1C1E', // iOS system background dark
];

const availableColors: { [key: string]: string[] } = {
  iosBlue,
  iosGray,
  // Keep legacy colors for backward compatibility
  shrimp: [
    '#f7f3f3',
    '#e6e4e4',
    '#cfc6c6',
    '#b9a5a5',
    '#a58988',
    '#9b7776',
    '#966d6d',
    '#835d5d',
    '#765252',
    '#694545',
  ],
  blueGray: iosGray,
  pnw: ['#f3f6f5', '#e8e9e9', '#cdd2cf', '#afbab3', '#95a69c', '#849a8c', '#7b9484', '#688071', '#5b7264', '#4a6354'],
  sahara: [
    '#fff1e7',
    '#f7e3d7',
    '#e9c5b2',
    '#daa689',
    '#cf8b66',
    '#c87a50',
    '#c57143',
    '#ae5f35',
    '#9c542d',
    '#894622',
  ],
  caribbean: [
    '#e3fdfc',
    '#d5f5f4',
    '#b0e9e6',
    '#88dcd8',
    '#66d1cb',
    '#50cac4',
    '#41c7c0',
    '#2eb0a9',
    '#1d9d97',
    '#008883',
  ],
  potato: [
    '#f7f3f2',
    '#e8e6e5',
    '#d2c9c6',
    '#bdaaa4',
    '#ab9087',
    '#a17f74',
    '#9d766a',
    '#896459',
    '#7b594e',
    '#6d4b40',
  ],
};

export const buildTheme = (primaryColor: string) => {
  const theme = createTheme({
    fontFamily: "-apple-system, BlinkMacSystemFont, 'SF Pro Display', 'SF Pro Text', 'Helvetica Neue', Helvetica, Arial, sans-serif",
    fontSizes: {
      xs: rem(11),
      sm: rem(13),
      md: rem(15),
      lg: rem(17),
      xl: rem(20),
    },
    lineHeights: {
      xs: '1.4',
      sm: '1.4',
      md: '1.5',
      lg: '1.5',
      xl: '1.6',
    },
    headings: {
      fontFamily: "-apple-system, BlinkMacSystemFont, 'SF Pro Display', 'SF Pro Text', 'Helvetica Neue', Helvetica, Arial, sans-serif",
      fontWeight: '600',
      sizes: {
        h1: { fontSize: rem(34), lineHeight: '1.2' },
        h2: { fontSize: rem(28), lineHeight: '1.3' },
        h3: { fontSize: rem(22), lineHeight: '1.3' },
        h4: { fontSize: rem(20), lineHeight: '1.4' },
        h5: { fontSize: rem(17), lineHeight: '1.4' },
        h6: { fontSize: rem(15), lineHeight: '1.4' },
      },
    },
    radius: {
      xs: rem(4),
      sm: rem(8),
      md: rem(10),
      lg: rem(12),
      xl: rem(16),
    },
    spacing: {
      xs: rem(4),
      sm: rem(8),
      md: rem(16),
      lg: rem(24),
      xl: rem(32),
    },
    shadows: {
      xs: '0 1px 2px 0 rgba(0, 0, 0, 0.05)',
      sm: '0 1px 3px 0 rgba(0, 0, 0, 0.1), 0 1px 2px 0 rgba(0, 0, 0, 0.06)',
      md: '0 4px 6px -1px rgba(0, 0, 0, 0.1), 0 2px 4px -1px rgba(0, 0, 0, 0.06)',
      lg: '0 10px 15px -3px rgba(0, 0, 0, 0.1), 0 4px 6px -2px rgba(0, 0, 0, 0.05)',
      xl: '0 20px 25px -5px rgba(0, 0, 0, 0.1), 0 10px 10px -5px rgba(0, 0, 0, 0.04)',
    },
    // @ts-expect-error type not exposed
    colors: availableColors,
    primaryColor: availableColors[primaryColor] ? primaryColor : 'iosBlue',
    defaultRadius: 'md',
    defaultGradient: {
      from: 'iosBlue',
      to: 'iosBlue',
      deg: 45,
    },
  });

  return theme;
};
