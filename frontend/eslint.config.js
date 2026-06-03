import js from '@eslint/js'
import { defineConfig, globalIgnores } from 'eslint/config'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'

const tsFiles = ['src/**/*.{ts,tsx}']

const architectureMessage = {
  publicApi: 'Import another slice only through its public API.',
  noUpwardImport:
    'FSD violation: a lower layer must not import from a higher layer.',
  noCrossSlice:
    'Cross-imports between slices of the same layer are not allowed. Use relative imports inside the same slice.',
  entitiesCrossSlice:
    'Cross-imports between entities must go through the @x notation. Use entities/*/@x/*.',
  deepRelative:
    'Deep relative import is forbidden. Use the @/ alias and the slice public API.',
}

const deepRelativePatterns = [
  {
    group: [
      '../../*',
      '../../../*',
      '../../../../*',
      '../../',
      '../../../',
      '../../../../',
    ],
    message: architectureMessage.deepRelative,
  },
]

const publicApiPatterns = [
  {
    group: [
      '@/pages/*/*',
      '@/widgets/*/*',
      '@/features/*/*',
      '@/entities/*/*',
    ],
    message: architectureMessage.publicApi,
  },
]

function withBaseImportRestrictions(extraPatterns = []) {
  return [
    'error',
    {
      patterns: [...deepRelativePatterns, ...extraPatterns],
    },
  ]
}

export default defineConfig([
  globalIgnores(['dist', 'build', 'coverage', 'node_modules']),
  {
    files: tsFiles,
    extends: [
      js.configs.recommended,
      ...tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
    linterOptions: {
      reportUnusedDisableDirectives: 'error',
    },
    rules: {
      'no-restricted-syntax': [
        'error',
        {
          selector: 'ExportDefaultDeclaration',
          message: 'Default exports are forbidden. Use named exports only.',
        },
      ],
      'react-refresh/only-export-components': [
        'warn',
        { allowConstantExport: true },
      ],
      '@typescript-eslint/no-unused-vars': [
        'error',
        {
          argsIgnorePattern: '^_',
          varsIgnorePattern: '^_',
          caughtErrorsIgnorePattern: '^_',
        },
      ],
      'no-restricted-imports': withBaseImportRestrictions(publicApiPatterns),
    },
  },
  {
    files: ['src/pages/**/*.{ts,tsx}'],
    rules: {
      'no-restricted-imports': withBaseImportRestrictions([
        {
          group: ['@/app/**'],
          message: architectureMessage.noUpwardImport,
        },
        {
          group: ['@/pages/**'],
          message: architectureMessage.noCrossSlice,
        },
        ...publicApiPatterns,
      ]),
    },
  },
  {
    files: ['src/widgets/**/*.{ts,tsx}'],
    rules: {
      'no-restricted-imports': withBaseImportRestrictions([
        {
          group: ['@/app/**', '@/pages/**'],
          message: architectureMessage.noUpwardImport,
        },
        {
          group: ['@/widgets/**'],
          message: architectureMessage.noCrossSlice,
        },
        ...publicApiPatterns,
      ]),
    },
  },
  {
    files: ['src/features/**/*.{ts,tsx}'],
    rules: {
      'no-restricted-imports': withBaseImportRestrictions([
        {
          group: ['@/app/**', '@/pages/**', '@/widgets/**'],
          message: architectureMessage.noUpwardImport,
        },
        {
          group: ['@/features/**'],
          message: architectureMessage.noCrossSlice,
        },
        {
          group: ['@/entities/*/*'],
          message: architectureMessage.publicApi,
        },
      ]),
    },
  },
  {
    files: ['src/entities/**/*.{ts,tsx}'],
    rules: {
      'no-restricted-imports': withBaseImportRestrictions([
        {
          group: ['@/app/**', '@/pages/**', '@/widgets/**', '@/features/**'],
          message: architectureMessage.noUpwardImport,
        },
        {
          group: [
            '@/entities/*/api/**',
            '@/entities/*/config/**',
            '@/entities/*/lib/**',
            '@/entities/*/model/**',
            '@/entities/*/ui/**',
          ],
          message: architectureMessage.entitiesCrossSlice,
        },
      ]),
    },
  },
  {
    files: ['src/shared/**/*.{ts,tsx}'],
    rules: {
      'no-restricted-imports': withBaseImportRestrictions([
        {
          group: [
            '@/app/**',
            '@/pages/**',
            '@/widgets/**',
            '@/features/**',
            '@/entities/**',
          ],
          message: architectureMessage.noUpwardImport,
        },
      ]),
    },
  },
  {
    files: ['src/shared/ui/**/*.{ts,tsx}'],
    rules: {
      'react-refresh/only-export-components': 'off',
    },
  },
  {
    files: [
      'src/pages/**/index.{ts,tsx}',
      'src/widgets/**/index.{ts,tsx}',
      'src/features/**/index.{ts,tsx}',
      'src/entities/**/index.{ts,tsx}',
    ],
    rules: {
      'no-restricted-syntax': [
        'error',
        {
          selector: 'ExportAllDeclaration',
          message: 'Do not use export *. Enumerate the slice public API explicitly.',
        },
      ],
    },
  },
])

