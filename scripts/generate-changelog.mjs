#!/usr/bin/env node

/**
 * Generates CHANGELOG.md from client/src/data/changelog.js.
 * Run: node scripts/generate-changelog.mjs
 */

import { writeFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { milestones, releases } from '../client/src/data/changelog.js';

const __dirname = dirname(fileURLToPath(import.meta.url));

const lines = [
  '# Changelog',
  '',
  'All notable changes to hush are documented here.',
  '',
  'Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)',
  '',
];

for (const release of releases) {
  const milestone = milestones.find((m) => m.id === release.milestone);
  const milestoneLabel = milestone ? ` — ${milestone.title}` : '';
  const currentTag = release.current ? ' (current)' : '';

  lines.push(`## [${release.version}] - ${release.date}${milestoneLabel}${currentTag}`);
  lines.push('');

  for (const group of release.groups) {
    const heading = group.label.charAt(0).toUpperCase() + group.label.slice(1);
    lines.push(`### ${heading}`);
    lines.push('');
    for (const item of group.items) {
      lines.push(`- ${item}`);
    }
    lines.push('');
  }
}

const outPath = resolve(__dirname, '..', 'CHANGELOG.md');
writeFileSync(outPath, lines.join('\n'), 'utf-8');
console.log(`Written to ${outPath}`);
