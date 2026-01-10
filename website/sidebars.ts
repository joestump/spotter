import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

// This runs in Node.js - Don't use client-side code here (browser APIs, JSX...)

/**
 * Creating a sidebar enables you to:
 - create an ordered group of docs
 - render a sidebar for each doc of that group
 - provide next/previous navigation

 The sidebars can be generated from the filesystem, or explicitly defined here.

 Create as many sidebars as you want.
 */
const sidebars: SidebarsConfig = {
  docsSidebar: [
    'intro',
    {
      type: 'category',
      label: 'Getting Started',
      items: [
        'getting-started/installation',
        'getting-started/configuration',
        'getting-started/docker',
      ],
    },
    {
      type: 'category',
      label: 'Features',
      items: [
        'features/listening-history',
        'features/playlist-management',
        'features/vibes-engine',
        'features/metadata-enrichment',
        'features/themes',
      ],
    },
    {
      type: 'category',
      label: 'Providers',
      items: [
        'providers/navidrome',
        'providers/spotify',
        'providers/lastfm',
      ],
    },
    {
      type: 'category',
      label: 'Enrichers',
      items: [
        'enrichers/overview',
        'enrichers/musicbrainz',
        'enrichers/spotify',
        'enrichers/lastfm',
        'enrichers/fanart',
        'enrichers/lidarr',
        'enrichers/openai',
      ],
    },
    {
      type: 'category',
      label: 'API Reference',
      items: [
        'api/endpoints',
        'api/authentication',
      ],
    },
    {
      type: 'category',
      label: 'Development',
      items: [
        'development/architecture',
        'development/contributing',
        'development/testing',
      ],
    },
  ],
};

export default sidebars;
