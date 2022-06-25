APP_PATH=/home/isucon/webapp
GO_PATH=/home/isucon/webapp/go

APP:=isucondition
DB_HOST:=127.0.0.1
DB_PORT:=3306
DB_USER:=isucon
DB_PASS:=isucon
DB_NAME:=isucondition
MYSQL_LOG:=/var/log/mysql/slow-query.log
MYSQL_ERR:=/var/log/mysql/error.log
NGINX_LOG:=/var/log/nginx/access.log
NGINX_ERR:=/var/log/nginx/error.log
GO_LOG:=/var/log/go.log

.PHONY: setup
setup:
	sudo apt update
	curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | sudo dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg
	echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | sudo tee /etc/apt/sources.list.d/github-cli.list > /dev/null
	sudo apt install -y percona-toolkit git unzip gh
	git init
	git config --global user.name karamaru-alpha
	git config --global user.email mrnk3078@gmail.com
	git config --global pull.rebase false
	git config credential.helper store
	curl -OL https://raw.githubusercontent.com/karamaru-alpha/isucon-memo/main/slow.sh
	sudo chmod +x ./slow.sh
	curl -OL https://raw.githubusercontent.com/karamaru-alpha/isucon-memo/main/analyze.sh
	sudo chmod +x ./analyze.sh
	wget https://github.com/matsuu/kataribe/releases/download/v0.4.1/kataribe-v0.4.1_linux_amd64.zip -O kataribe.zip
	# wget https://github.com/matsuu/kataribe/releases/download/v0.4.1/kataribe-v0.4.1_linux_arm.zip -O kataribe.zip
	unzip -o kataribe.zip
	sudo mv kataribe /usr/local/bin/
	sudo chmod +x /usr/local/bin/kataribe
	sudo rm kataribe.zip
	kataribe -generate
	sudo sed -i -e "s/slow_count[ \f\n\r\t]*=.*/slow_count = 10/" kataribe.toml
	sudo sed -i -e "s/show_stddev[ \f\n\r\t]*=.*/show_stddev = false/" kataribe.toml
	sudo sed -i -e "s/show_status_code[ \f\n\r\t]*=.*/show_status_code = false/" kataribe.toml
	sudo sed -i -e "s/show_bytes[ \f\n\r\t]*=.*/show_bytes = false/" kataribe.toml
	sudo sed -i -e "s/percentiles[ \f\n\r\t]*=.*/percentiles = []/" kataribe.toml
	sudo rm README.md 2> /dev/null
	sudo rm LICENSE 2> /dev/null
	gh auth login
# GitHub.com -> SSH -> /home/isucon/.ssh/id_rsa.pub -> Paste an authentication token -> https://github.com/settings/tokens

.PHONY: before
before:
	cd $(APP_PATH)
	git checkout . && git clean -df .
	git rev-parse --abbrev-ref HEAD | xargs echo "BRANCH:"
	git rev-parse --abbrev-ref HEAD | xargs git pull origin
	sudo cp my.cnf /etc/mysql/my.cnf
	sudo cp nginx.conf /etc/nginx/nginx.conf
	sudo cp $(APP).conf /etc/nginx/sites-enabled/$(APP).conf
	(cd $(GO_PATH) && go build -o $(APP))
	sudo rm -f $(NGINX_LOG)
	sudo rm -f $(NGINX_ERR)
	sudo rm -f $(MYSQL_LOG)
	sudo rm -f $(MYSQL_ERR)
	sudo rm -f $(GO_LOG)
	sudo systemctl restart nginx
	sudo systemctl restart mysql
	sudo systemctl restart $(APP).go.service

.PHONY: before-db
before-db:
	git stash
	git pull origin main
	sudo cp my.cnf /etc/mysql/my.cnf
	sudo rm $(MYSQL_LOG) 2> /dev/null
	sudo touch $(MYSQL_LOG)
	sudo chown -R mysql $(MYSQL_LOG)
	sudo systemctl restart mysql
	sudo systemctl stop nginx
	sudo systemctl stop $(APP).go.service

.PHONY: slow
slow:
	sudo $(APP_PATH)/slow.sh $(MYSQL_LOG)
# sudo pt-query-digest $(MYSQL_LOG) -limit=5 --report-format=query_report // --filter='$event->{arg} =~ m/^select/i'

.PHONY: kataru
kataru:
	sudo cat $(NGINX_LOG) | kataribe -f $(APP_PATH)/kataribe.toml

.PHONY: log
log:
	sudo cat $(GO_LOG)

.PHONY: fetch
fetch:
	cd $(APP_PATH) && git fetch origin main && git reset --hard origin/main

.PHONY: sql
sql:
	mysql -h$(DB_HOST) -P$(DB_PORT) -u$(DB_USER) -p$(DB_PASS) $(DB_NAME)
	# docker-compose exec mysql bash -c 'mysql -uisucon -pisucon isucari'
