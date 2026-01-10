import clsx from 'clsx';
import Link from '@docusaurus/Link';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';
import Heading from '@theme/Heading';

import styles from './index.module.css';

function HomepageHeader() {
  const {siteConfig} = useDocusaurusContext();
  return (
    <header className={clsx('hero hero--primary', styles.heroBanner)}>
      <div className="container">
        <Heading as="h1" className="hero__title">
          {siteConfig.title}
        </Heading>
        <p className="hero__subtitle">{siteConfig.tagline}</p>
        <div className={styles.buttons}>
          <Link
            className="button button--secondary button--lg"
            to="/docs/">
            Get Started
          </Link>
          <Link
            className="button button--outline button--lg"
            style={{marginLeft: '1rem'}}
            href="https://github.com/joestump/spotter">
            View on GitHub
          </Link>
        </div>
      </div>
    </header>
  );
}

type FeatureItem = {
  title: string;
  description: JSX.Element;
};

const FeatureList: FeatureItem[] = [
  {
    title: 'Unified Listening History',
    description: (
      <>
        Aggregate your listening history from Navidrome, Spotify, and Last.fm
        into a single, unified view. Never lose track of what you've been
        listening to across services.
      </>
    ),
  },
  {
    title: 'AI-Powered Vibes Engine',
    description: (
      <>
        Create DJ personas with unique personalities that curate personalized
        mixtapes based on your listening history. Let AI discover new
        combinations from your library.
      </>
    ),
  },
  {
    title: 'Metadata Enrichment',
    description: (
      <>
        Automatically enrich your music library with metadata from MusicBrainz,
        Spotify, Last.fm, Fanart.tv, and OpenAI. Get detailed artist bios,
        album artwork, and smart tags.
      </>
    ),
  },
  {
    title: 'Playlist Syncing',
    description: (
      <>
        Sync playlists from Spotify and Last.fm to your Navidrome library.
        Intelligent track matching ensures your external playlists work with
        your local collection.
      </>
    ),
  },
  {
    title: 'Real-time Updates',
    description: (
      <>
        Server-Sent Events push new listens and sync notifications to your
        browser in real-time. No manual refreshing needed.
      </>
    ),
  },
  {
    title: 'Retro-Themed UI',
    description: (
      <>
        Choose between a warm 1970s music cabinet aesthetic or an 1980s
        cyberpunk neon theme. Both themes are carefully crafted for an
        immersive experience.
      </>
    ),
  },
];

function Feature({title, description}: FeatureItem) {
  return (
    <div className={clsx('col col--4')}>
      <div className="padding-horiz--md padding-vert--lg">
        <Heading as="h3">{title}</Heading>
        <p>{description}</p>
      </div>
    </div>
  );
}

function HomepageFeatures(): JSX.Element {
  return (
    <section className={styles.features}>
      <div className="container">
        <div className="row">
          {FeatureList.map((props, idx) => (
            <Feature key={idx} {...props} />
          ))}
        </div>
      </div>
    </section>
  );
}

export default function Home(): JSX.Element {
  const {siteConfig} = useDocusaurusContext();
  return (
    <Layout
      title={`${siteConfig.title} - AI-Powered Playlist Generator`}
      description="AI-powered playlist generator for Navidrome. Aggregate listening history, generate mixtapes with AI DJ personas, and enrich your music library.">
      <HomepageHeader />
      <main>
        <HomepageFeatures />
        <section className={styles.quickStart}>
          <div className="container">
            <div className="row">
              <div className="col col--8 col--offset-2">
                <Heading as="h2" className="text--center margin-bottom--lg">
                  Quick Start
                </Heading>
                <div className={styles.codeBlock}>
                  <pre>
                    <code>
{`# Clone the repository
git clone https://github.com/joestump/spotter.git
cd spotter

# Install dependencies
make deps

# Configure your environment
cp .env.example .env
# Edit .env with your Navidrome URL and API keys

# Run the server
make run`}
                    </code>
                  </pre>
                </div>
                <p className="text--center margin-top--lg">
                  <Link to="/docs/getting-started/installation">
                    View full installation guide →
                  </Link>
                </p>
              </div>
            </div>
          </div>
        </section>
      </main>
    </Layout>
  );
}
