/** Rsbuild replaces this at build time. An ordinary build remains desktop. */
export const isWebEdition = process.env.PUBLIC_ASTRLINK_EDITION === "web";
