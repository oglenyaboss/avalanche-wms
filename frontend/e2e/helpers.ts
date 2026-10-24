import { expect, type Page } from "@playwright/test";

export async function loginAs(page: Page, username: string, password = username) {
  await page.goto("/login");
  await page.getByLabel("Логин").fill(username);
  await page.getByLabel("Пароль").fill(password);
  await page.getByRole("button", { name: "Войти" }).click();
  await expect(page.getByTestId("current-user")).toHaveText(username);
}
