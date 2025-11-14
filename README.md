**Fork Notice:** This repository is a fork of the original `surmai` project. See the project's documentation
for full installation and usage details.

# Surmai

Surmai is a travel organizer web application focused on collaborative trip planning, offline access, and privacy-aware
data storage. It provides features for organizing trip details, collaborating with others, and keeping travel artifacts in one place.

## Features

- Organize trip information in one place
- Collaboration support for multiple users
- Offline access and mobile-friendly UI
- Privacy-first data handling
- Optional AI-powered itinerary assistance

## Mobile / PWA

Surmai is implemented as a Progressive Web App (PWA) and can be installed on many mobile platforms. For general PWA
information see the MDN guide: https://developer.mozilla.org/en-US/docs/Web/Progressive_web_apps/Guides/What_is_a_progressive_web_app

## Demo

A public demo instance may be available at https://demo.surmai.app/ (if present, the demo site is managed by the project maintainers and may be reset periodically). No personal demo credentials are published in this forked copy.

## Installation

See the project's documentation for installation and deployment instructions, including Docker-based setups:
http://surmai.app/documentation/installation

### AI itinerary assistant setup (optional)

The optional trip assistant feature relies on an external API and requires an API key. If you enable that feature set the required environment variable for your deployment. For example (POSIX shells):

```bash
export OPENAI_API_KEY=sk-your-key
```

Ensure your key has access to the API you intend to use. The frontend should not directly expose secrets — proxy such
requests through the authenticated backend.

## Credits

This project integrates several open-source tools and datasets. Notable mentions:

- Backend platform: PocketBase (https://pocketbase.io/)
- UI library: Mantine (https://mantine.dev/)
- Airport data: OurAirports (https://ourairports.com/data/)
- City data: countries-states-cities-database (https://github.com/dr5hn/countries-states-cities-database)
- Airlines dataset: dotmarn/Airlines (https://github.com/dotmarn/Airlines)
- Currency data: ExchangeRate-APIs (https://www.exchangerate-api.com/docs/free)

If you need additional information from the original repository, consult the upstream project.
