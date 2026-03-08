# what-the-claude

Simple Claude Code proxy app with native gui to capture Claude Code API traffic and display 
remaining token limits in the system menu.
Always have an eye on your usage limits and when they reset.

MacOS system menu bar entry, showing 15% usage of the 5h window, with a reset in about 3h
(tooltip shows time to reset for both the 5h and the 7d limit).

<img width="100" alt="image" src="https://github.com/user-attachments/assets/781571d8-6e47-43d3-9560-4484895b4b82" />
<br/>
<br/>
<br/>


First, build and run the app
```
go build .

./what-the-claude
```

then start claude code cli with 

```
ANTHROPIC_BASE_URL=http://127.0.0.1:6543 claude
```



Build and package as MacOS app
```
go install fyne.io/tools/cmd/fyne@latest

fyne package
```


Session data and settings are stored in `~/.what-the-claude`
