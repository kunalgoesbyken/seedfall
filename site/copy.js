const COPIED_FOR_MS = 1500;

const attach = (block) => {
  const text = block.textContent.trim();
  const button = document.createElement("button");
  button.type = "button";
  button.className = "copy";
  button.textContent = "copy";
  button.addEventListener("click", async () => {
    await navigator.clipboard.writeText(text);
    button.textContent = "copied";
    setTimeout(() => { button.textContent = "copy"; }, COPIED_FOR_MS);
  });
  block.append(button);
};

document.querySelectorAll("[data-copy]").forEach(attach);
