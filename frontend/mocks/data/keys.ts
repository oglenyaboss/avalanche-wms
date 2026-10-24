export type SeededKey = { username: string; credentialId: string; jwk: JsonWebKey };

export const seededKeys: SeededKey[] = [
  {
    username: "operator",
    credentialId: "LPOsHC0GGIJ02P84OCSFSg",
    jwk: {
      kty: "EC",
      crv: "P-256",
      x: "QZUDtGmdCQdK5lgFSm_9wRVu1EMuJht4uX6HAjEG6rc",
      y: "RomZ4-ta-atCGvM9IavseAWM-AmZpwhg4EaCTBlTB5s",
      d: "NEQDwGEX5I_cuxKcU_TftnyGwHmQ0-jdUHvtN83ozuE",
    },
  },
  {
    username: "admin",
    credentialId: "c9ssfWosmRTAsqhx6mpAUQ",
    jwk: {
      kty: "EC",
      crv: "P-256",
      x: "Gr-6bE-ilbTRwDVT129ZsNy1VHFF3PT4GGrXKHPRFZ4",
      y: "UU49H8H_03HW_ldejXvcgPe8gHbkcP_Py5xriikYVWE",
      d: "Zc3-k5l-8_BhmF97WxzZ5yiGWIBL8KT8msGgLYk5BzI",
    },
  },
];
