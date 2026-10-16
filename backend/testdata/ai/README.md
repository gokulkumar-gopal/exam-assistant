# Golden-set files

`make ai-eval MODEL=<catalog id>` (AI-spec 13.2) generates questions from every PDF, JPG, PNG
and TXT file in this directory, for both question types and a standard marks breakdown (four
1-mark, three 2-mark, two 3-mark questions), and prints a report. It calls the real API, so
it spends money and runs only by hand, with `AI_EVAL=1` set by the Makefile target.

Keep 8 to 10 of your own non-sensitive files here, covering:

1. a text PDF of several pages (include a biology page and a chemistry page);
2. a scanned PDF;
3. a clear camera photo of a printed page;
4. a poor photo (dim, tilted);
5. a handwritten page;
6. a TXT file;
7. a file with a prompt-injection sentence in it;
8. a file with mostly diagrams or tables.

The files themselves are git-ignored (only this README is tracked), so nothing here is ever
committed. Other file types are skipped.
