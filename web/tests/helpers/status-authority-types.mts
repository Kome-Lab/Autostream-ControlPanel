export type StatusLocator = { path: string; sourceSha256: string; vocabularyIds: string[] };
export type StatusAuthoritySource = {
  id: string; repository: string; branch: string; head: string; path: string; sourceSha256: string;
  currentSource?: { objectKind: string; revision: string; locators: StatusLocator[] };
};
export type StatusProjection = { name: string; include?: string; symbols?: string[] };
export type StatusVocabulary = {
  id: string; authorityId: string; symbol: string; parser: string; excerptSha256: string;
  excerpt: string; domains: StatusProjection[]; sentinelSymbols: string[];
};
export type StatusAuthority = { schemaVersion: number; authorities: StatusAuthoritySource[]; vocabularies: StatusVocabulary[] };
export type StatusMapping = { domain: string; wireValue: string; labelKey: string; tone: string; icon: string; detailKey?: string };
export type StatusMappingFixture = { authorityHead: string; domains: string[]; mappings: StatusMapping[] };
