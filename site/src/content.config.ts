import { defineCollection, z } from 'astro:content';
import { glob } from 'astro/loaders';

const docs = defineCollection({
  loader: glob({ pattern: '**/*.md', base: './src/content/docs' }),
  schema: z.object({
    title: z.string(),
    lang: z.string(),
    slug: z.string().optional(),
    order: z.number().optional(),
    description: z.string().optional(),
  }),
});

const blog = defineCollection({
  loader: glob({ pattern: '**/*.md', base: './src/content/blog' }),
  schema: z.object({
    title: z.string(),
    lang: z.string(),
    date: z.string(),
    tag: z.string().optional(),
    readTime: z.string(),
    excerpt: z.string(),
    description: z.string().optional(),
  }),
});

const tips = defineCollection({
  loader: glob({ pattern: '**/*.md', base: './src/content/tips' }),
  schema: z.object({
    title: z.string(),
    lang: z.string(),
    date: z.string(),
    excerpt: z.string(),
    description: z.string().optional(),
  }),
});

export const collections = { docs, blog, tips };
